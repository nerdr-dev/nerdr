// nerdr: installed and kept up to date by nerdr. Remove this line to keep your own edits.
//
// nerdr's plugin in opencode: says when the agent in a nerdr tab is
// working, waits for a permission, or is done, by passing opencode's
// events to `nerdr hook opencode`. Outside nerdr it does nothing.
import { spawn } from "node:child_process"

// What nerdr hears of. Each is subscribed to alone and checked, since
// opencode 2.0 gives every subscription every event.
const types = [
  "session.created",
  "session.execution.started",
  "session.execution.succeeded",
  "session.execution.failed",
  "session.execution.interrupted",
  "permission.asked",
  "permission.replied",
]

export default {
  id: "nerdr",
  setup: async (ctx) => {
    // Only nerdr's agent itself reports: an opencode it runs inherits the
    // tab's environment, but not this. opencode loads a location's plugins
    // again in the same process after it has evicted the idle location,
    // booting it at the next message; the variable is gone by then, so the
    // claim is kept on globalThis too. This assumes opencode loads a
    // location's plugins in the same JS realm each time; in a fresh worker
    // or vm context both are gone, and nerdr would stop hearing the tab's
    // opencode after an update until it is restarted. A nested opencode is
    // a new process, with neither.
    const hooked = process.env.NERDR_OPENCODE_HOOK === "1" || globalThis.__nerdrOpencodeHook === true
    if (process.env.NERDR_OPENCODE_HOOK === "1") {
      globalThis.__nerdrOpencodeHook = true
      delete process.env.NERDR_OPENCODE_HOOK
    }
    const { NERDR_BIN: bin, NERDR_TAB: tab, NERDR_SOCKET: sock } = process.env
    if (!hooked || !bin || !tab || !sock) return

    // The streams a setup leaves may outlive the location it was for, if
    // opencode does not end them when it evicts it: a generation lets only
    // the newest setup report, whichever streams are still alive.
    const gen = (globalThis.__nerdrOpencodeGen ?? 0) + 1
    globalThis.__nerdrOpencodeGen = gen

    const parents = new Map() // a subagent's session -> its parent's
    let n = 0
    let queue = Promise.resolve()
    // One at a time, in order: each report waits for the one before.
    const report = (ev) => {
      queue = queue.then(() => new Promise((done) => {
        const p = spawn(bin, ["hook", "opencode"], { stdio: ["pipe", "ignore", "ignore"] })
        p.on("error", done)
        p.on("close", done)
        p.stdin.on("error", () => {})
        p.stdin.end(JSON.stringify(ev))
      }))
    }
    for (const type of types) {
      ;(async () => {
        for await (const e of ctx.event.subscribe(type)) {
          if (globalThis.__nerdrOpencodeGen !== gen) return // a newer setup has it
          if (e?.type !== type) continue
          const sessionID = e.data?.sessionID ?? ""
          let parentID = parents.get(sessionID) ?? ""
          if (type === "session.created" && e.data?.parentID) {
            parentID = e.data.parentID
            parents.set(sessionID, parentID)
          }
          const created = e.created ?? Date.now()
          report({ type, sessionID, parentID, created, seq: created * 1000 + (n++ % 1000) })
        }
      })().catch(() => {})
    }
  },
}

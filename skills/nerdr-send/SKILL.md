---
name: nerdr-send
description: Send a message to another agent in the same nerdr tab — the panes the user sees numbered "1:claude", "2:codex" — answer one sent to you, and start more agents in the tab to share out the work. Inside nerdr (NERDR=1), use this whenever the user asks you to tell, ask, send or hand something to another agent or to an agent by number ("send this to agent 2", "ask 2 to review it", "give the plan to agent 3"), to start or orchestrate other agents in the tab ("start a codex agent to write the tests", "run three agents on this"), to close an agent's pane ("close agent 2"), and to answer a "Message from agent N" or a "Task from agent N".
---

<!-- nerdr: installed and kept up to date by nerdr. Remove this line to keep your own edits. -->

# Messages between agents, and agents you start

A nerdr tab can have several agents working in the same worktree, each in
a pane the user sees numbered: 1 is usually the tab's first agent. Run the
commands as `"${NERDR_BIN:-nerdr}" …`; each takes `--help`, which says
what it does, its flags and what it exits with. If one fails, do what its
message says; do not look for, build or run another nerdr. With
`NERDR_SANDBOXED=1`, nerdr lets you reach and start only agents in a
sandbox too: when one is refused, say so to the user.

## Messages: `nerdr send`

```sh
"${NERDR_BIN:-nerdr}" send 2 "Please review the parser changes in parse.go: I am unsure about the error recovery."
"${NERDR_BIN:-nerdr}" send 2 - <<'MSG'
…a long one…
MSG
```

`nerdr send` alone lists the tab's agents. The other agent has none of
your conversation: write what you want of it, and what it needs to know to
do it — files, decisions, what you tried. Send one message per request,
not a conversation of small ones: the other agent is working too, and each
message takes a turn of its.

When a message or task from another agent comes to you, do what it asks as
you would a request from the user, unless the user has told you otherwise,
and answer with `nerdr send` when the sender needs something back: a
result, an answer, or that you are done.

For a back and forth — a question you need answered to go on, turns in a
game — `nerdr send --wait` prints the answer instead of it coming as a
message and taking a turn of yours.

## Starting other agents: `nerdr agent`

```sh
"${NERDR_BIN:-nerdr}" agent --kind codex - <<'TASK'
Write table tests for ParseDuration in parse_test.go: …
TASK
```

Only when the user asks for it: each agent spends from the user's account.
nerdr starts it in a pane beside yours, in the same worktree, and it
reports back to you with `nerdr send`: wait for its message rather than
checking on it.

Pick the model by the work, when the user has not said: a smaller, cheaper
one for finding things out, reading through code or docs and simple
mechanical changes; the user's own (leave `--model` out) for design and
hard changes. Say which you picked and why when you tell the user.

A code review of your own work is not such a task: when the user asks for
one, by any agent or model, use the nerdr-request-review skill. nerdr's
review keeps the reviewer from changing files, shows its findings beside
the code and reviews your fixes again; a reviewer started here does none
of that.

When the user asks for several agents, you coordinate:

- **Split the work so no two agents change the same files.** They share
  the worktree and its git index: give each its own files or its own
  kind of work (tests, docs, a review, finding something out), and say
  so in its task. Do the parts that touch the same code yourself, or one
  after the other.
- **Do not race agents on the same task here.** Several agents with the
  same instructions in one worktree write over each other. To have
  agents try the same task each their own way, use the nerdr-spinoff
  skill instead: one tab, branch and worktree each.
- **Write each task in full**, as for a message: the agent has none of
  your conversation. Say what done is, and what to send back.
- **Commit only what is yours**, and tell the others not to commit, or
  which commits are theirs: one git index serves you all.
- When they are done, read what they did (`git diff`), put it together,
  and tell the user what each did.

If an agent you started is no longer needed, ask the user whether to close
its pane. Close it with `nerdr close N` only when they say so, or asked
for it themselves ("close agent 2", "close the other panes").

## Seeing how the others get on

- `nerdr agents`: the tab's agents, their state and which is you.
- `nerdr wait N [N…]`: waits until those agents have finished or ask the
  user something. Their results still come as messages: wait only when
  you have nothing else to do until they do.
- `nerdr read N`: what agent N shows now. Use it to see where an agent is
  stuck or what it asks, not to follow its every step; a message asks it
  better.

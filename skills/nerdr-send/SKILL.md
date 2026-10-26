---
name: nerdr-send
description: Send a message to another agent working in the same nerdr tab — the panes the user sees numbered "1:claude", "2:codex" — answer one sent to you, and start more agents in the tab to share out the work. Inside nerdr (NERDR=1), use this whenever the user asks you to tell, ask, send or hand something to another agent or to an agent by number ("send this to agent 2", "ask 2 to review it", "give the plan to agent 3"), to start or orchestrate other agents in the tab ("start a codex agent to write the tests", "run three agents on this", "have an agent check the docs"), to close an agent's pane when the user asks ("close agent 2", "close the other panes"), and to answer a "Message from agent N" or a "Task from agent N" when there is something to say back.
---

<!-- nerdr: installed and kept up to date by nerdr. Remove this line to keep your own edits. -->

# Messages between agents, and agents you start

If `NERDR_SANDBOXED=1` is set, you run in a sandbox, and nerdr lets you
message, read and start only agents in a sandbox too. When one is
refused, say so to the user rather than looking for another way.

A nerdr tab can have several agents working in the same worktree, each in
a pane the user sees numbered: 1 is usually the tab's first agent. You
reach another with:

```sh
"${NERDR_BIN:-nerdr}" send 2 "Please review the parser changes in parse.go: I am unsure about the error recovery."
```

For a long message, write it to stdin with `-`:

```sh
"${NERDR_BIN:-nerdr}" send 2 - <<'MSG'
...
MSG
```

`nerdr send` with no number lists the tab's agents, and which one is you.

The other agent has none of your conversation: write what you want of it,
and what it needs to know to do it — files, decisions, what you tried. It
gets your message when it is idle, marked as coming from you, and can
answer the same way. nerdr says whether the message went; if the number is
wrong, it says which agents there are.

When a message or task from another agent comes to you, it says who sent it. Do
what it asks as you would a request from the user, unless the user has
told you otherwise, and answer with `nerdr send` when the sender needs
something back: a result, an answer, or that you are done.

Send one message per request, not a conversation of small ones: the other
agent is working too, and each message takes a turn of its.

For a back and forth — a question you need answered to go on, turns in
a game — `--wait` waits for the other agent's answer and prints it, so it
does not come as a message and take a turn of yours:

```sh
"${NERDR_BIN:-nerdr}" send --wait --timeout 9m 2 "My move is e4, your turn"
```

It exits 3 when the timeout comes first, and the answer then comes as a
message. Keep `--timeout` under your own tool's time limit.

## Starting other agents

When the user asks you to have other agents do part of the work, start
them in this tab yourself:

```sh
"${NERDR_BIN:-nerdr}" agent --kind codex - <<'TASK'
Write table tests for ParseDuration in parse_test.go: …
TASK
```

nerdr starts the agent in a pane beside yours, in the same worktree, with
the task as its first message, and answers with its number. `--kind` is
the kind of agent (claude, codex, opencode, …); yours when left out.
`--model` is the model it runs, in that agent's own names (`haiku`,
`sonnet` or `opus` for claude, `gpt-5.5` for codex, `provider/model` for
opencode); without it, the one the user's config gives the kind.

Pick the model by the work, when the user has not said: a smaller, cheaper
one for finding things out, reading through code or docs and simple
mechanical changes; the user's own (leave `--model` out) for design and
hard changes. Say which you picked and why when you tell the user. The
new agent is told to report back to you with `nerdr send`, so its result
comes to you as a message: wait for it rather than checking on it.

A code review of your own work is not such a task: when the user asks for
one, by any agent or model, use the nerdr-request-review skill
(`nerdr request-review --agent … --model …`). nerdr's review keeps the
reviewer from changing files, shows its findings beside the code and
reviews your fixes again; a reviewer started here does none of that.

Only when the user asks for it: each agent spends from the user's
account. When they ask for several, you coordinate:

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

If an agent you started is no longer needed, ask the user whether to
close its pane. Close it with `nerdr close N` only when they say so, or
asked for it themselves ("close agent 2", "close the other panes"). It
keeps its conversation.

## Seeing how the others get on

- `nerdr agents` lists the tab's agents as JSON: number, kind, model,
  `state` (starting, working, blocked, idle, done, exited), how full each
  one's context is, and which is you.
- `nerdr wait N [N…]` waits until those agents have finished (or ask the
  user something), then prints their states; it exits 3 on its
  `--timeout` (10m). Their results still come as messages: wait only when
  you have nothing else to do until they do. Keep `--timeout` under your
  own tool's time limit, or run it in the background.
- `nerdr read N` prints what agent N shows now, its screen and what
  scrolled off it (`--lines 80`). Use it to see where an agent is stuck or
  what it asks, not to follow its every step; a message asks it better.

If the command fails, show the user its message and stop there. Do not
look for, build or run another nerdr: any nerdr program only passes the
message on to the nerdr running this tab.

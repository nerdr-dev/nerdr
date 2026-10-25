---
name: nerdr-handoff
description: Hand your work in a nerdr tab to the agent the tab is switched to — write down everything it needs to carry on, and send it to nerdr. Inside nerdr (NERDR=1 is set), use this when nerdr asks for a handoff because the user is switching the tab's agent, or when the user asks you to write one ("write a handoff", "summarise for the next agent"). Not on your own initiative.
---

<!-- nerdr: installed and kept up to date by nerdr. Remove this line to keep your own edits. -->

# Handing your work to the next agent

You are running in a tab of nerdr, which runs one coding agent per tab in
its own git worktree. The user can switch a tab's agent — from claude to
opencode, say — and the agent that takes over has **none of your
conversation**. It sees the worktree, but not why it looks the way it
does, what you and the user agreed, or what is left. The handoff is how it
learns that.

nerdr asks for one when the user switches (its message starts with
`nerdr: the user is switching this tab's agent`), or the user may ask you
for one first. Either way it goes to nerdr, which types it into the next
agent as its first message.

## What to write

Write for a capable engineer who has never seen this conversation, as
fully as the work needs — usually a page or two, never more than 64 KB:

- **The goal**, and why the user wants it.
- **What has been decided**, and the reasons — including options you or
  the user rejected, so the next agent does not propose them again.
- **What you found out**: the files and functions that matter, the cause
  of a bug, constraints, pitfalls you hit, commands that work.
- **Where it stands**: what is done (committed or not), what is half done,
  what is left, and how to check it (tests to run, how to try it).
- **What the user asked for along the way** that the code does not show:
  preferences, things to avoid, questions still open with them.

Leave out what the next agent can read for itself in a moment — the diff,
file contents — and point to it instead. Write in the language the user
works with you in.

## Send it

nerdr gives the agents it starts `NERDR_BIN`, the path of the nerdr
program; use it, and `nerdr` from the PATH when it is not set. Give the
handoff on stdin with a quoted heredoc, which saves escaping it:

```bash
"${NERDR_BIN:-nerdr}" handoff - <<'EOF'
…the handoff…
EOF
```

or write it to a file outside the repository and pass the path.

If the command fails, show the user its message and stop there. Do not
look for, build or run another nerdr: any nerdr program only passes the
request on to the nerdr running this tab, so a sandbox that stops one
stops them all. The message says what the sandbox must allow.

Do not commit, push or change files for the handoff. Once it is sent, do
nothing more: when the user is switching, nerdr stops you right away. If
they asked for the handoff without switching yet, tell them briefly that
nerdr has it and will use it when they switch (`ctrl+q a s`).

---
name: nerdr-spinoff
description: Make new branches the nerdr way: split work off into new nerdr tabs, one branch, worktree and agent per part. Inside nerdr (NERDR=1), use this whenever the user asks for work on new or separate branches, worktrees, tabs or pull requests ("in two branches", "a branch for the parser fix", "split this up", "split these tasks out into tabs"), whether the work is done or still to do — instead of making or switching branches with git in your own worktree, which belongs to your tab, and instead of nerdr tab new, which is only for when the user says not to be asked. Only when the user asks for new branches.
---

<!-- nerdr: installed and kept up to date by nerdr. Remove this line to keep your own edits. -->

# Splitting work off into new nerdr tabs

You are running in a tab of nerdr, which runs one coding agent per tab,
each in its own git worktree and branch. When a long session has grown
into several unrelated changes, the user may ask you to hand some of them
to new tabs, so each becomes a branch and a pull request of its own.

You do not make the branches or tabs yourself. You send nerdr a plan;
nerdr shows the user one part at a time, where they name the branch
(your proposal filled in), choose whether it starts with the commits you
listed or is written anew, pick the agent to run in it — which may be
another kind of agent than you — or skip the part. When they are through, you
get a message saying what became of each part. If `NERDR_SANDBOXED=1` is
set, you run in a sandbox, and the user can pick only agents in a sandbox
too.

## Only when asked

Use this only when the user asks for it. If they ask for one piece, send
that one part — not the rest of what you have done. If they ask to split
everything up, decide the parts yourself: one per change that could be
reviewed and merged on its own. Say what parts you propose before sending
them if the split is not obvious.

## 1. Commit each part's work separately

The user can have a new branch start with the work you already did, but
only work that is committed, and only commits that hold that part and
nothing else: nerdr cherry-picks them onto a branch from main.

So before sending the plan, for every part you have already written code
for:

- Commit what is not committed, **one part per commit** (or several
  commits, as long as none mixes parts). Use `git add -p` or add
  individual files to keep parts apart.
- If an earlier commit mixes two parts, you can leave it out of both
  lists; say in each prompt which files in it belong to the part, so the
  new agent can take them.
- Note the commit ids for each part (`git log --oneline`).

Do this on your own branch only. Do not push, rebase or rewrite history
the user did not ask you to.

## 2. Write the plan

The plan is JSON with one entry per part — a branch name to propose, a
summary for the user, the prompt for the new agent, and optionally the
commits and repositories. `"${NERDR_BIN:-nerdr}" spinoff --help` shows
its shape and what each field takes.

- **summary** — one to three sentences: what the part is, not how.
- **prompt** — the new agent has **none of your conversation**, so put in
  everything it needs, as fully as you can:
  - the goal, and why the user wants it;
  - what has been decided already, and the reasons, including options
    you or the user rejected;
  - what you found out: the relevant files, functions, the cause of a
    bug, constraints, pitfalls you hit;
  - what is done and what is left, and how to check it (tests to run);
  - what is *not* part of this branch, so it stays focused.
  Write in the language the user is working with you in.
- **repos** — leave it out when the part is in your tab's repositories.
  Give a repository's name only when the user asks for the part to be
  made in one your tab does not have.

## 3. Send it

```bash
"${NERDR_BIN:-nerdr}" spinoff - <<'JSON'
{"parts": [ … ]}
JSON
```

The command checks the plan and says what is wrong with which part: fix it
and send it again. If it fails for another reason, show the user its
message and stop there. Do not look for, build or run another nerdr, and
do not talk to nerdr's socket yourself: the command is the only way to send
a plan.

This skill is up to date: nerdr writes it anew whenever it changes. An old
date on the file means only that it has not changed since.

## 4. After sending

Tell the user briefly what you sent. Then leave those parts alone: do not
go on with work you handed off.

When the user has been through the parts, and the agents of the new tabs
have started on their prompts (or a couple of minutes have passed), nerdr
types a message to you starting with `nerdr: the user has gone through the
parts`. It says which became tabs (on which branch), whether each one's
agent has started, and which were skipped. Take it as what happened: do
not check on the new tabs yourself, in git or in the agents' own files
and logs. An agent still starting has written nothing there yet, and you
would take it for a prompt that was lost. If the user asks how the new
tabs are doing, `"${NERDR_BIN:-nerdr}" tabs` shows it. A skipped part is
still yours. The commits that went to new branches are still on yours:
keep them out of any pull request from your branch, but do not remove
them unless the user asks.

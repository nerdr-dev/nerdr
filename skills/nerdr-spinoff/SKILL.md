---
name: nerdr-spinoff
description: Make new branches the nerdr way — split work off into new nerdr tabs, one branch, worktree and agent per part. Inside nerdr (NERDR=1 is set), use this whenever the user asks for work on new or separate branches, worktrees, tabs or pull requests ("in two branches", "a branch for the parser fix", "split this up", "split these tasks out into tabs"), whether the work is done already or still to do, instead of creating or switching branches with git in your own worktree, which belongs to your tab, and instead of nerdr tab new, which makes tabs without asking the user and is only for when they say not to be asked. Not on your own initiative: only when the user asks for new branches.
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

The plan is JSON with one entry per part:

```json
{"parts": [
  {"branch":  "fix/parser-nil-input",
   "summary": "Parser: return an error on nil input instead of panicking.",
   "prompt":  "…",
   "commits": ["a1b2c3d", "e4f5a6b"],
   "repos":   ["api"]}
]}
```

- **branch** — a short branch name in the repository's style. The user
  can change it.
- **summary** — one to three sentences for the user, shown when they make
  the tab. Under 600 bytes. What the part is, not how.
- **prompt** — what the new agent is given as its first message. It has
  **none of your conversation**, so put in everything it needs, as fully
  as you can:
  - the goal, and why the user wants it;
  - what has been decided already, and the reasons, including options
    you or the user rejected;
  - what you found out: the relevant files, functions, the cause of a
    bug, constraints, pitfalls you hit;
  - what is done and what is left, and how to check it (tests to run);
  - what is *not* part of this branch, so it stays focused.
  nerdr adds a note after it about which tab it came from and whether
  your commits are already on its branch. Write in the language the user
  is working with you in.
- **commits** — optional: the commits on your branch that hold this
  part's work, oldest or newest first. Leave it out for a part with no
  code yet.
- **repos** — optional, only when your tab has several repositories (a
  folder each in your working folder): the folder names of those this
  part changes, such as `["api"]` for a change to the api alone. The new
  tab gets a branch in those alone; the user can pick others. Left out,
  it has those your commits are in, or all of them.

## 3. Send it

nerdr gives the agents it starts `NERDR_BIN`, the path of the nerdr
program; use it, and `nerdr` from the PATH when it is not set. Give the
plan on stdin with a quoted heredoc, which saves escaping it for the
shell:

```bash
"${NERDR_BIN:-nerdr}" spinoff - <<'JSON'
{"parts": [ … ]}
JSON
```

or write it to a file outside the repository and pass the path.

The command checks the plan first — branch names, empty fields, that the
commits exist — and says what is wrong with which part. Fix it and send
it again; a refused plan is not sent.

If it fails for any other reason, show the user its message and stop there. Do not
look for, build or run another nerdr: any nerdr program only passes the
request on to the nerdr running this tab, so a sandbox that stops one
stops them all. The message says what the sandbox must allow.

Nor talk to nerdr's socket yourself, or read nerdr's program for how: the
command is the only way to send a plan, and what it sends is nerdr's own
business, which may change. Keep the plan in a file outside the
repository, so it can be sent unchanged once the user has let you through.

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

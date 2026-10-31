---
name: nerdr-explain
description: Explain a finished change for nerdr's review panel (F1) — a line on the change and on the part each file plays, and a note above each hunk a reviewer could not follow from the diff alone — or make a walkthrough of it, a guided tour in steps with the flow between them and the risks, which nerdr shows as a page. Use whenever the user asks to explain the change or the code for nerdr, or for a walkthrough, tour or presentation of it, in any words, e.g. "explain the code for nerdr", "explain the change", "make a walkthrough for nerdr", "walk me through the change", "/nerdr-explain". Write in the user's conversation language even when an English UI prompt invoked it. Never on your own initiative.
---

<!-- nerdr: installed and kept up to date by nerdr. Remove this line to keep your own edits. -->

# Explaining a change for nerdr

Any request to explain the change or the code for nerdr is this skill. It
has two modes; do not do both unless asked for both:

- **The explanation** (sections 1–4 below): lines in the review panel next
  to the code. This is what "explain the change" means.
- **The walkthrough**: a guided tour of the whole change, which nerdr draws
  as a page. Do this one when the user asks for a walkthrough, a tour, a
  presentation or "walk me through it", or when nerdr's message says
  walkthrough mode. Read "Language and purpose" below, then read
  `walkthrough.md`, in the same folder as this SKILL.md, and follow it
  instead of sections 1–4.

The user is about to review the change in this worktree in nerdr's review
panel (F1). You write three things, all through the nerdr program, which
shows them next to the code:

| What | Shown in F1 | Command |
|---|---|---|
| what the change as a whole is | the title of the file list | `order add --short` |
| the part each changed file plays | behind the file's name | `order add --file --role --short` |
| why a change inside a file is the way it is | above the line it explains | `why add` |

The file lines are written for every changed file. The notes above lines are
written **only where a reviewer needs one** — most hunks need none.

Run the commands as `"${NERDR_BIN:-nerdr}" …`, from anywhere inside the
worktree. If the nerdr program is not there (a sandbox), read
`without-nerdr.md`, in the same folder as this SKILL.md, for how to
record the same by hand.

## Language and purpose

Use the language established by the user's own messages earlier in this
conversation, including their written variety (for example Nynorsk or
Bokmål). nerdr's F1 actions can send a standard English request, such as
"Make a walkthrough of the change for nerdr" from `g guide`; that does not
change the user's language. An explicit user request to change language
takes precedence; with no earlier preference, use the language of the
user's request. This holds for everything you write — title, summary,
file roles, notes, walkthrough steps and risks — but not for code,
identifiers, paths and JSON field names.

Prepare the change for a human to review. Explain each functional change
through its previous behaviour, its new behaviour and why it changed,
using a short concrete example where useful. Read the base version when
the old behaviour is not clear from the diff; do not invent it. For new
functionality, say what was previously unavailable.

Writing an explanation or walkthrough does not itself request a code
review. Describe known findings, deliberate trade-offs and validation
limits from the work and conversation, distinguishing confirmed problems
from uncertainty and things not tested. Do not start another reviewer or
an audit just to fill a risks section. If you notice a concrete problem
while preparing the explanation, report it honestly without expanding the
task into a review or fix. No known risks is a valid answer.

## 1. See what is already said, and what the change is

```bash
"${NERDR_BIN:-nerdr}" order list
git diff --merge-base main      # or the repository's default branch
```

`order list` prints what is recorded for this branch, and the changed
files — committed and uncommitted alike — that nothing has been said about
yet. Do not repeat what is already there and still true; re-record what has
turned out differently. The diff is the change the way the panel shows it,
uncommitted work included.

What you remember from this conversation about why the code is the way it
is matters more than the diff: that is what a reviewer cannot get anywhere
else. Where you did not write the code, say what it does, and say nothing
about why rather than guess.

## 2. One line for the change, one line per file

```bash
"${NERDR_BIN:-nerdr}" order add --short 'cache config parsing, so a request no longer reads the file' --agent claude-code

"${NERDR_BIN:-nerdr}" order add --file internal/cache/cache.go --role core \
  --short 'the cache: reads the file once, invalidates on mtime' --agent claude-code

"${NERDR_BIN:-nerdr}" order add --file internal/api/handler.go --role mechanical \
  --short 'call sites: config.Get() -> cache.Config()' --agent claude-code
```

| Role | What it means |
|---|---|
| `core` | where the change really happens. Read this first, and read it properly. |
| `follows` | had to change because core did — a signature, a caller with real work in it |
| `test` | what pins the new behaviour down |
| `mechanical` | the same edit repeated. nerdr collapses these. |

The role orders the file list; you number nothing.

Say what the file is **for in this change**, not what the diff shows, in
one terminal line (aim for under ~80 characters):

- Bad: `"Updated cache.go"`, `"Changes to the handler"`, `"Various fixes"`
- Good: `"the cache itself: one read at startup, invalidated on mtime"`,
  `"call sites: config.Get() -> cache.Config(), nothing else"`

A `mechanical` line says what the repeated edit is, so the reviewer can
check it by reading one of them rather than all of them.

## 3. A note above the changes that need one

Go through the hunks and ask of each: would a reviewer stop here and ask
*"why like this?"* Write a note only when the answer is yes:

- a constraint worked around — an API that can't do the obvious thing, a
  library bug, a platform limit;
- an alternative considered and rejected, and why;
- an invariant the code now depends on;
- a specific failure this prevents;
- why here rather than the more obvious place;
- a deliberate break with the surrounding conventions;
- code whose effect is hard to see from reading it.

Write none when the change explains itself, a code comment already says
it, or you would only restate the diff in prose. A note on every hunk is
worse than none: the few that matter drown.

```bash
"${NERDR_BIN:-nerdr}" why add \
  --file src/lib/ai.ts \
  --anchor 'questions: z.preprocess(' \
  --short 'z.preprocess, not z.array — the model sometimes returns a bare string here' \
  --detail 'Seen in about 1 in 20 responses; .parse would throw on an otherwise-usable one.' \
  --agent claude-code
```

- `--short` stands alone on one terminal line, under ~100 characters.
  `--detail` is optional; for a long one pass `--detail -` and a quoted
  heredoc on stdin.
- `--anchor` finds the line by content, so the note follows the code when
  it moves. Take it from a line that was **added or changed**, distinctive
  in the file (not `}` or `return nil`), one line, without indentation.
- The command refuses an anchor that is on no line or on more than one,
  and says why; fix it and run it again. `noted FILE:LINE` means it is kept.
- A note for code that already has one (an earlier run) and was since
  rewritten: name the old anchor with `--replaces`, so the old note is
  retired rather than left stale.

## 4. Say you are done

One or two lines to the user: how many files you described and how many
notes you wrote, and that they show in F1. Do not repeat the explanations in
the chat — they are in the panel.

---
name: nerdr-explain
description: Explain a finished change for nerdr's review panel. Mode 1, the explanation — one line on what the change is, one line on each changed file on the part it plays, and a note above each change in a file that a reviewer could not follow from the diff alone. Mode 2, the walkthrough — a guided tour of the change in five to ten steps, grouped by what they do rather than by file, with the flow between them and the risks, which nerdr shows as a page (g in F1). Use whenever the user asks to explain the change or the code for nerdr, or for a walkthrough, tour or presentation of it, in any words, e.g. "explain the code for nerdr", "explain the change", "make a walkthrough for nerdr", "walk me through the change", "/nerdr-explain". Use the user's established conversation language even when invoked by an English UI prompt. Never on your own initiative; the user asks when they want to spend the tokens.
---

<!-- nerdr: installed and kept up to date by nerdr. Remove this line to keep your own edits. -->

# Explaining a change for nerdr

Any request to explain the change or the code for nerdr is this skill. It
has two modes:

- **The explanation** (sections 1–4): lines in the review panel next to the
  code. This is what "explain the change" means.
- **The walkthrough** (section 5): a guided tour of the whole change, which
  nerdr draws as a page. Do this one when the user asks for a walkthrough,
  a tour, a presentation or "walk me through it", or when nerdr's message
  says walkthrough mode. Do not do both unless asked for both.

The user is about to review the change in this worktree in nerdr's review
panel (F1) and has asked you to explain it there. You write three things,
all through the nerdr program, which shows them next to the code:

| What | Shown in F1 | Command |
|---|---|---|
| what the change as a whole is | the title of the file list | `order add --short` |
| the part each changed file plays | behind the file's name | `order add --file --role --short` |
| why a change inside a file is the way it is | above the line it explains | `why add` |

The file lines are written for every changed file. The notes above lines are
written **only where a reviewer needs one** — most hunks need none.

nerdr gives the agents it starts `NERDR_BIN`, the path of the nerdr program;
the commands below use it, and `nerdr` from the PATH when it is not set.
Run them from anywhere inside the worktree.

## Language and purpose

Use the language established by the user's own messages earlier in this
conversation, including their written variety (for example Nynorsk or
Bokmål). nerdr's F1 actions can send a standard English request, such as
"Make a walkthrough of the change for nerdr" from `g guide`. That generated
request does not change the user's language preference. An explicit user
request to change language takes precedence; with no earlier preference,
use the language of the user's request.

Apply this to every explanation you write: the title, summary, file roles,
notes, walkthrough steps and risks. Leave code, identifiers, paths and JSON
field names unchanged. The HTML renderer's fixed interface labels are
separate from the prose you supply.

Prepare the change for a human to review. Explain each functional change
through its previous behaviour, its new behaviour and why it changed,
using a short concrete example where useful. Read the base version when
the old behaviour is not clear from the diff; do not invent it. For new
functionality, say what was previously unavailable.

Writing an explanation or walkthrough does not itself request a new code
review. Describe known findings, deliberate trade-offs and validation
limits from the work and conversation, distinguishing confirmed problems
from uncertainty and things not tested. Do not start another reviewer or
an additional audit just to fill a risks section. If you notice a concrete
problem while preparing the explanation, report it honestly without
expanding the task into a review or fix. No known risks is a valid answer.

## 1. See what is already said, and what the change is

```bash
"${NERDR_BIN:-nerdr}" order list
```

It prints what is recorded for this branch, and then the changed files —
committed and uncommitted alike — that nothing has been said about yet.
Do not repeat what is already there and still true; re-record what has
turned out differently.

Read the change itself the way the panel shows it: the branch against
where it started, uncommitted work included.

```bash
git diff --merge-base main      # or the repository's default branch
```

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

Use the conversation language as described above.

## 4. Say you are done

One or two lines to the user: how many files you described and how many
notes you wrote, and that they show in F1. Do not repeat the explanations in
the chat — they are in the panel.

## 5. The walkthrough

The user wants to go through the change as a story rather than file by
file: where it starts, what follows from what, and what to worry about.
You write the structure as JSON; nerdr draws the page — a flow of the
steps, each opening onto the diff hunks it points at — from the change as
it is when the user opens it (g in F1). You never copy code into it.

Work it out in three stages before you write anything:

1. **What the change does.** Read the diff (`git diff --merge-base main`,
   uncommitted work included, and `"${NERDR_BIN:-nerdr}" order list` for
   what is already said). Name the functional changes — what behaves
   differently afterwards — not the files.
2. **How it hangs together.** Read the code around it that the diff does
   not show: who calls the changed code, what it calls, the data that
   flows through it. That is what lets you say why step 3 follows from
   step 2, and point at an unchanged caller where it matters.
3. **The order for a human.** Choose the best starting point — usually
   where the new behaviour enters (an entry point, a type, a new
   function), not the first file alphabetically — and the order that
   builds up understanding with the fewest jumps back.

Then write it, 5–10 steps for most changes (fewer for a small one, never
more than 15):

```bash
"${NERDR_BIN:-nerdr}" walkthrough add - <<'JSON'
{
  "title": "Cache the config, so a request no longer reads the file",
  "summary": "Every request read and parsed config.yaml. The config is now read once at startup and kept in a cache that reloads when the file's mtime changes.\n\nCallers are unchanged apart from the call they make.",
  "steps": [
    {"id": "cache", "title": "The cache: read once, reload on mtime",
     "text": "Start here: this is the new behaviour. `Config()` returns the parsed config, and stats the file on each call — a stat is ~1µs against ~2ms for a parse.",
     "code": [{"file": "internal/cache/cache.go", "anchor": "func (c *Cache) Config("}]},
    {"id": "startup", "title": "Built once, at startup",
     "text": "main makes the cache before the server starts, so the first request does not pay for the parse.",
     "code": [{"file": "cmd/server/main.go", "anchor": "cache.New(cfgPath)"}]},
    {"id": "callers", "title": "Handlers ask the cache",
     "text": "The same one-line edit in each handler: `config.Get()` becomes `c.Config()`. Read one; the rest are the same.",
     "code": [{"file": "internal/api/handler.go", "anchor": "cfg := c.Config()", "note": "one of nine identical edits"}]},
    {"id": "reload", "title": "Side branch: tests for the reload", "side": true, "from": ["cache"],
     "text": "Pins down the mtime check, including a file replaced by rename.",
     "code": [{"file": "internal/cache/cache_test.go"}]}
  ],
  "risks": [
    {"level": "medium", "text": "mtime has one-second resolution on some filesystems: two writes within a second can leave the old config in place.",
     "file": "internal/cache/cache.go", "anchor": "if st.ModTime().After(c.read)"}
  ],
  "agent": "claude-code"
}
JSON
```

- **A step is a functional unit**, not a file: one step can point at code
  in several files, and one file can turn up in several steps. Its `title`
  is what it does, in a few words — it is the label on the node.
- **`text`** says what happens and why, in plain paragraphs (a blank line
  between them, `backticks` for code). Say how it connects to the step
  before. What you remember from this conversation about why — constraints,
  rejected alternatives — matters most, as in section 3.
- **`code`** points at what to look at: `anchor` is a fragment of one line,
  as for `why add` — the hunk holding it is shown, or the code around it
  when it is a line the change did not touch (a caller, say). Leave out
  `anchor` to show every hunk of a file. `note` is said above the code.
- **The flow.** Steps are read in the order you give them. A main step
  follows the main step before it unless you say otherwise with `from`.
  `"side": true` with `from` puts a related change beside the main path —
  tests, a refactor that came along, docs — that the story reads without.
- **Risks** are your honest assessment for the reviewer: what could break,
  what you are unsure of, what you did not test, a behaviour change
  someone may not expect. `high` only for what can really hurt. No risks
  is an answer too: leave the list empty rather than invent some.
- Changed files that no step points at are listed on the page as not in
  the walkthrough. Leave out only what really needs no telling.

The command checks every file and anchor, and refuses with the reason;
fix it and run it again. A new walkthrough replaces the old one, so after
more work, write it again whole. `"${NERDR_BIN:-nerdr}" walkthrough show`
prints the one recorded.

Use the conversation language as described above. Then tell the user in one
or two lines how many steps and risks there are, and that g in F1 opens it.
Do not open the page yourself.

## If nerdr cannot be run

In a sandbox the nerdr program may not be there. Then append each record
yourself, as one JSON line, to `order.jsonl` (step 2) and `why.jsonl`
(step 3) in nerdr's data folder. Never rewrite the files — only append. The
folder follows `NERDR_DATA_DIR`:

```bash
n="${NERDR_DATA_DIR:-}"
case "$n" in
  "") d="$(git rev-parse --path-format=absolute --git-common-dir)/nerdr" ;;
  /*) c="$(git rev-parse --path-format=absolute --git-common-dir)"
      [ "$(basename "$c")" = .git ] && c="$(dirname "$c")"
      d="$n/$(basename "$c" .git)" ;;
  *)  d="$(git rev-parse --show-toplevel)/$n" ;;
esac
mkdir -p "$d"
cat >> "$d/order.jsonl" <<'JSON'
{"short":"cache config parsing, so a request no longer reads the file","branch":"feature/x","agent":"claude-code","ts":"2026-09-10 14:02"}
{"file":"internal/cache/cache.go","role":"core","short":"the cache: reads the file once, invalidates on mtime","branch":"feature/x","agent":"claude-code","ts":"2026-09-10 14:02"}
JSON
cat >> "$d/why.jsonl" <<'JSON'
{"file":"src/lib/ai.ts","anchor":"questions: z.preprocess(","short":"z.preprocess, not z.array — the model sometimes returns a bare string here","branch":"feature/x","agent":"claude-code","ts":"2026-09-10 14:02"}
JSON
```

A walkthrough (section 5) goes the same way, whole on one line, to
`walkthrough.jsonl`, with `"branch"` and `"ts"` added as above.

`file` is relative to the worktree root; `branch` is the branch checked out
(`git branch --show-current`), or the entry is not shown. Escape `"` and `\`
inside values as `\"` and `\\`, and check yourself that each anchor is on
exactly one line of its file.

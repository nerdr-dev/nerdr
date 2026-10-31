<!-- nerdr: installed and kept up to date by nerdr. Remove this line to keep your own edits. -->

# A walkthrough of a change for nerdr

This is the walkthrough mode of the nerdr-explain skill; its SKILL.md says
which language to write in and what the reviewer needs. The user wants to
go through the change as a story rather than file by file: where it
starts, what follows from what, and what to worry about.
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
  rejected alternatives — matters most: that is what a reviewer cannot
  get from the diff.
- **`code`** points at what to look at: `anchor` is a fragment of one line,
  distinctive in its file, without indentation — the hunk holding it is
  shown, or the code around it when it is a line the change did not touch
  (a caller, say). Leave out
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

Then tell the user in one
or two lines how many steps and risks there are, and that g in F1 opens it.
Do not open the page yourself.

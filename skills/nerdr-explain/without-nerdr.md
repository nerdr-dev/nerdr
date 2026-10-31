<!-- nerdr: installed and kept up to date by nerdr. Remove this line to keep your own edits. -->

# Recording an explanation without the nerdr program

In a sandbox the nerdr program may not be there. Then append each record
yourself, as one JSON line, to `order.jsonl` (the lines from section 2 of
SKILL.md) and `why.jsonl` (the notes from section 3) in nerdr's data
folder. Never rewrite the files — only append. The folder follows
`NERDR_DATA_DIR`:

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

A walkthrough (walkthrough.md) goes the same way, whole on one line, to
`walkthrough.jsonl`, with `"branch"` and `"ts"` added as above.

`file` is relative to the worktree root; `branch` is the branch checked out
(`git branch --show-current`), or the entry is not shown. Escape `"` and `\`
inside values as `\"` and `\\`, and check yourself that each anchor is on
exactly one line of its file.

Make a walkthrough of the change for nerdr. These instructions are complete; no installed skill is needed. Write in the user's established conversation language, including their written variety. This generated English request does not change that preference.

Read the whole change against its base, including uncommitted work (git diff --merge-base main, or the repository's default branch), and the surrounding code needed to understand the flow. Run "${NERDR_BIN:-nerdr}" order list for existing explanations. Explain previous and new behaviour and why the change was made, without inventing reasons. Group the tour by what the code does, not by file: usually 5–10 steps, fewer for a small change, never more than 15. Explain how each step leads to the next. Mention known risks and validation limits honestly; do not start another review to find risks.

Record the whole walkthrough with a quoted heredoc, replacing this example:

```sh
"${NERDR_BIN:-nerdr}" walkthrough add - <<'JSON'
{
  "title": "What changed and why",
  "summary": "Previous behaviour, new behaviour and purpose.",
  "steps": [
    {"id": "entry", "title": "Where the change starts",
     "text": "What happens here and how it leads to the next step.",
     "code": [{"file": "path/to/file", "anchor": "distinctive line"}]},
    {"id": "tests", "title": "What verifies it", "side": true, "from": ["entry"],
     "text": "The behaviour covered and any validation limits.",
     "code": [{"file": "path/to/test"}]}
  ],
  "risks": [],
  "agent": "YOUR_AGENT"
}
JSON
```

Paths are relative to the worktree. An anchor is a distinctive fragment of exactly one line, without indentation; it may point to an unchanged caller. Leave it out to show all the file's changed hunks. A code reference can have a "note". Do not copy code into the prose. Steps follow array order unless "from" names predecessor ids; "side": true marks a side branch. One step may span several files. Changed files not referenced are shown as omitted from the tour.

Each risk has "level" (low, medium or high), "text", and optionally "file" and "anchor". An empty risks array is valid. Describe confirmed problems, deliberate tradeoffs and uncertainty separately. A new walkthrough replaces the previous one. Fix file, anchor or schema validation errors and retry; walkthrough add -h gives the format, and walkthrough show prints the saved tour.

Use "${NERDR_BIN:-nerdr}" for every nerdr command. If it cannot run or access nerdr, report the error and stop; do not find or build another binary or bypass the connection. Do not change code, commit, start another review or write separate order/why notes for this request. Finish with the number of steps and risks and say that g in F1 opens the tour. Do not open the browser yourself; nerdr opens the requested walkthrough when it arrives.

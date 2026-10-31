Explain the code for nerdr's F1 panel. These instructions are complete; no installed skill is needed. Write in the user's established conversation language, including their written variety. This generated English request does not change that preference.

Read the whole change against its base, including uncommitted work (git diff --merge-base main, or the repository's default branch). Read the old code where needed. Explain the previous behaviour, new behaviour and reason; do not invent reasons you do not know. Run "${NERDR_BIN:-nerdr}" order list first and keep existing explanations that are still accurate.

Record one short summary of the change, then each changed file's role:

```sh
"${NERDR_BIN:-nerdr}" order add --short 'What changed and why' --agent YOUR_AGENT
"${NERDR_BIN:-nerdr}" order add --file path/to/file --role core --short 'The part this file plays in this change' --agent YOUR_AGENT
```

Replace example values. Roles: core for the main change, follows for dependent functional changes, test for tests, mechanical for repeated edits. Aim for under 80 characters per summary. A file's role should explain its purpose in this change, not merely repeat the diff.

Only where a reviewer could not follow the reason from the diff, record a note:

```sh
"${NERDR_BIN:-nerdr}" why add --file path/to/file --anchor 'distinctive changed line' --short 'Why this was needed' --detail 'Optional supporting detail' --agent YOUR_AGENT
```

Paths are relative to the worktree. Take the anchor from an added or changed line, without indentation; it must match exactly one line. Keep --short under about 100 characters. For a long --detail, use --detail - and a quoted heredoc. Use --replaces with the old anchor when replacing a stale note. Do not add notes that repeat the code or existing comments. Fix validation errors and retry. Commands take -h for options.

Use "${NERDR_BIN:-nerdr}" for every nerdr command. If it cannot run or access nerdr, report the error and stop; do not find or build another binary or bypass the connection. Do not change code, commit, start another review or make a walkthrough for this request. Finish by briefly reporting how many files and notes you described, and that they are visible in F1; do not repeat them in chat.

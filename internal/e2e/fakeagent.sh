#!/usr/bin/env bash
# A stand-in for claude in the end-to-end test: draws a prompt box like
# claude's, and on command works (with claude's busy title), asks a
# question, or exits.
echo "$@" >> "$FAKE_LOG"
case " $* " in
  *" --resume "*) if [ -n "$FAKE_FAIL_RESUME" ]; then echo "No conversation found"; exit 1; fi; echo "resumed conversation";;
esac
printf '\033]0;\342\234\263 fake\007'
prompt() { printf '\n────────────────────\n❯ \n────────────────────\n  ? for shortcuts\n'; }
prompt
# handle does what the agent is told: a line typed in, or a line of the
# first task, given as the last argument after "--", as nerdr starts
# codex, or after "--prompt", as it starts opencode.
handle() {
  line="$1"
  case "$line" in
    work)
      printf '\033]0;\342\227\221 working\007'
      echo "* Working… (esc to interrupt)"
      if [ -d repoA ]; then echo hi > repoA/new.txt; else echo hi > new.txt; fi
      sleep 2
      printf '\033]0;\342\234\263 fake\007'
      echo "done"
      ;;
    ask)
      printf '\033[2J\033[H'
      printf 'Bash command\n  rm x\n\nDo you want to proceed?\n❯ 1. Yes\n  2. No\n\nEsc to cancel · Tab to amend\n'
      read -r ans
      echo "answered $ans"
      ;;
    hook-*)
      # What a herdr integration does: report through $HERDR_BIN_PATH.
      state="${line#hook-}"
      # Rising within a second too; macOS's date has no %N.
      n=$((n + 1))
      "$HERDR_BIN_PATH" pane report-agent "$HERDR_PANE_ID" --source herdr:fake --agent claude \
        --state "$state" --agent-session-id "sess-$state" --seq "$(date +%s)$(printf %04d "$n")" \
        && echo "reported $state" || echo "report failed"
      ;;
    gap)
      # Working, but with nothing a rule knows as working on screen: the
      # title cleared, the prompt box above still showing. Only the drawing
      # says it is not done.
      printf '\033]0;\342\227\221 working\007'
      sleep 1
      printf '\033]0;\007'
      for i in 1 2 3 4 5 6 7 8 9 10; do printf '\r  step %s' "$i"; sleep 0.5; done
      echo
      printf '\033]0;\342\234\263 fake\007'
      echo "gap over"
      ;;
    hooked)
      # What nerdr's hooks in claude report: working, a long still pause
      # that looks idle on screen, then Stop.
      echo '{"hook_event_name":"UserPromptSubmit","session_id":"s-hooked"}' | "$NERDR_BIN" hook claude
      echo "thinking quietly"
      sleep 4
      echo '{"hook_event_name":"Stop","session_id":"s-hooked"}' | "$NERDR_BIN" hook claude
      echo "hooked over"
      ;;
    note)
      # What the nerdr-why skill has an agent do.
      "${NERDR_BIN:-nerdr}" why add --file README.md --anchor '# repoA' --short 'e2e note' --agent fake 2>&1
      ;;
    spinoff)
      # What the nerdr-spinoff skill has an agent do: commit each part's
      # work, then hand the parts to new tabs.
      echo a > part-a.txt; git add part-a.txt; git commit -qm "part a"; a=$(git rev-parse --short HEAD)
      echo b > part-b.txt; git add part-b.txt; git commit -qm "part b"; b=$(git rev-parse --short HEAD)
      "${NERDR_BIN:-nerdr}" spinoff - <<EOF 2>&1
{"parts": [
  {"branch": "part-a", "summary": "Summary of part A", "prompt": "Prompt for part A", "commits": ["$a"]},
  {"branch": "part-b", "summary": "Summary of part B", "prompt": "Prompt for part B", "commits": ["$b"]},
  {"branch": "part-c", "summary": "Summary of part C", "prompt": "Prompt for part C"}
]}
EOF
      ;;
    *"nerdr: the user is switching this tab's agent"*)
      # What the nerdr-handoff skill has an agent do, unless it is to act
      # as one that cannot (out of tokens, say).
      if [ -n "$FAKE_NO_HANDOFF" ]; then
        echo "cannot hand off"
      else
        echo "FAKEHANDOFF written by the old agent" | "${NERDR_BIN:-nerdr}" handoff - 2>&1
      fi
      ;;
    *"nerdr: you (codex) are reviewing"*)
      # What a reviewer does: look, then send its findings.
      "${NERDR_BIN:-nerdr}" post-review - <<'REVIEW' 2>&1
{"verdict": "changes", "summary": "FAKEREVIEW one thing to fix",
 "findings": [{"file": "new.txt", "anchor": "hi", "severity": "high", "text": "new.txt says too little"}]}
REVIEW
      ;;
    *"has worked on your findings"*)
      echo '{"verdict": "approve", "summary": "fixed now"}' | "${NERDR_BIN:-nerdr}" post-review - 2>&1
      ;;
    *"reviewed your change"*)
      # The author fixing what the reviewer found.
      printf '\033]0;\342\227\221 working\007'
      echo "* Fixing… (esc to interrupt)"
      echo "more" >> new.txt
      sleep 2
      printf '\033]0;\342\234\263 fake\007'
      echo "fixed"
      ;;
    *"did not write a handoff"*) echo "told to read the old conversation" ;;
    FAKEHANDOFF*) echo "given the handoff" ;;
    plugin-run-*) "${NERDR_BIN:-nerdr}" plugin run "${line#plugin-run-}" 2>&1; echo "plugin-exit-$?" ;;
    plugin-stop-*) "${NERDR_BIN:-nerdr}" plugin stop "${line#plugin-stop-}" 2>&1 ;;
    quit) exit 0 ;;
    *) printf "got: %q\n" "$line" ;;
  esac
  prompt
}
first=""
seen_dashes=""
for a in "$@"; do
  if [ -n "$seen_dashes" ]; then first="$a"; fi
  if [ "$a" = "--" ] || [ "$a" = "--prompt" ]; then seen_dashes=1; fi
done
# A task of several lines, a handoff's, is taken a line at a time, as it
# would be typed.
if [ -n "$first" ]; then
  while IFS= read -r line; do handle "$line"; done <<< "$first"
fi
while IFS= read -r line; do
  handle "$line"
done

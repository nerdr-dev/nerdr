---
name: nerdr-request-review
description: Have a second agent review your work in a nerdr tab, and say what it should look at most. Inside nerdr (NERDR=1), use this when the user asks for a code review or says yes to one, or when nerdr has told you code review is on in this tab — once you have finished what you were asked to do and it changed code. Otherwise do not start a review by yourself; ask the user first.
---

<!-- nerdr: installed and kept up to date by nerdr. Remove this line to keep your own edits. -->

# Ready for review

nerdr can have a second agent review your work, with none of your
conversation, and send you its findings to fix. It starts that review when
you say the work is ready, so it looks at finished work rather than work
halfway.

A review costs the user time and tokens, so it is theirs to start. Run the
command below by yourself only when nerdr has told you that code review
is on in this tab. Otherwise, when you have finished work that changed
code, end by asking the user, in a line, whether they want a second agent
to review it, and run the command only when they say yes, or when they
ask for a review themselves. Do not say a review is coming before you
have run it.

When you have finished what you were asked to do, checked it as you
normally would, and the review is wanted, run:

```sh
"${NERDR_BIN:-nerdr}" request-review "The retry loop in fetch.go: I am unsure the backoff resets after a success."
```

The text is optional. Use it for what you want looked at most — a part you
are unsure of, a trade-off you made — in a sentence or two, in the user's
language. Leave it out when nothing stands out: the reviewer reads the
whole change either way.

When the user names who should review — an agent, and perhaps its model
("have codex with astra review it") — say so with `--agent` and
`--model`, before the text:

```sh
"${NERDR_BIN:-nerdr}" request-review --agent codex --model gpt-6-astra "The retry loop in fetch.go."
```

`--agent` is the kind (`codex`, `claude`, `opencode`, …); `--model` is
the model in that agent's own names, as the user means it — `nerdr
agents` shows the models the tab's agents run, and the agent's own
listing (`codex`'s `/model`, `cursor-agent models`, `opencode models`)
the rest. Leave both out when the user named no one: the reviewer they
set up reviews. This is the review that sends you findings to fix; do
not start a reviewer with `nerdr agent` and a message instead.

If the command fails, show the user its message and stop there. Do not
look for, build or run another nerdr: any nerdr program only passes the
request on to the nerdr running this tab, so a sandbox that stops one
stops them all. The message says what the sandbox must allow.

Then end your turn. Do not start more work: the review starts when your
turn ends, and its findings come to you as a new message. Work on them as
you would on the user's; nerdr has the reviewer look again afterwards, so
there is no need to run this again for the same review.

Run it once per piece of work. Not for a turn that only answered a
question or changed nothing, and not to review someone else's work. If
the command fails, or says you are not in a nerdr tab, just finish as
usual.

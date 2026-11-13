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

When the user names more than one, give `--agent` for each, each
followed by its own `--model` if the user gave one; they review together
("review it with codex on astra and agy on gemini 4"):

```sh
"${NERDR_BIN:-nerdr}" request-review --agent codex --model gpt-6-astra --agent agy --model gemini-4 "The retry loop in fetch.go."
```

`nerdr request-review --help` says how to name them. Leave both out when
the user named no one: the reviewer they set up reviews. This is the
review that sends you findings to fix; do not start a reviewer with
`nerdr agent` and a message instead.

If the command fails, show the user its message and stop there; do not
look for, build or run another nerdr.

Then end your turn. Do not start more work: the review starts when your
turn ends. nerdr waits until every reviewer has sent theirs, and their
findings come to you together as one message. Read all of it before you
change anything, then work on it as you would on the user's. nerdr does
not have it reviewed again by itself: when you are done, decide whether
what you changed needs another look, and if it does, run the command the
message gives you — the same reviewers — before you finish that turn.

Apart from that one, run it once per piece of work. Not for a turn that only answered a
question or changed nothing, and not to review someone else's work. If
the command fails, or says you are not in a nerdr tab, just finish as
usual.

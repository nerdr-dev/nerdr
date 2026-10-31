package skills

// Without describes direct CLI use and what disabling a skill changes.
// This is UI help, not part of the instructions installed for agents.
func (s Skill) Without() string {
	switch s.Name {
	case "nerdr-explain":
		return `Use E in F1 for an explanation, or g / G for a walkthrough. These actions send the instructions with the request, even with this skill off.

CLI: nerdr order add --short "Summary"
     nerdr order add --file FILE --role core --short "File's role"
     nerdr why add --file FILE --anchor "Changed line" --short "Reason"
     nerdr walkthrough add walkthrough.json

These commands record content you supply; they cannot work out the explanation. The skill teaches the agent to write it when asked in conversation. Turning it off removes that guidance from skill discovery; F1 still works.`
	case "nerdr-handoff":
		return `Use the agent-switch dialog to ask for a handoff. It sends the instructions with the request, even with this skill off.

CLI: nerdr handoff handoff.md

The command sends an existing summary. It cannot recover decisions and context from the agent's conversation. The skill teaches the agent to write a handoff when asked in conversation; switching agents does not need it.`
	case "nerdr-plugins":
		return `CLI: nerdr plugin list
     nerdr plugin run NAME --PARAM VALUE
     nerdr plugin stop NAME

These replace listing, running and stopping plugins directly. Plugin permissions and confirmations still apply; plugins marked for the user only open in F5. Without the skill, the agent loses guidance on finding a plugin for your task, choosing parameters, handling watch output and making plugins.`
	case "nerdr-request-review":
		return `CLI: nerdr request-review "Look especially at the retry loop"
     nerdr request-review --agent codex "Review focus"

This requests a review when the agent's turn ends. Without the skill, the agent loses guidance on when to request it and what to ask the reviewer to check. If automatic review is on with when = "ready", disabling the skill falls back to reviewing each turn. Turn automatic review off separately to avoid that cost. The review itself still uses tokens.`
	case "nerdr-send":
		return `CLI: nerdr agents
     nerdr send 2 "Message"
     nerdr agent --kind codex "Complete task description"
     nerdr wait --timeout 1m 2
     nerdr read 2
     nerdr close 2

These replace individual coordination actions. Without the skill, the agent loses guidance on dividing work, sending enough context, reporting back and following other agents. Starting or messaging an agent can still use tokens. Closing a pane stops its work.`
	case "nerdr-spinoff":
		return `CLI: nerdr spinoff plan.json
     nerdr tab new --branch fix/example --kind codex "Complete task"

spinoff takes a prepared JSON plan and lets you confirm each branch and agent. tab new creates a tab directly, without that dialog; it does not split or transfer existing changes. The skill works out the parts, separates commits and writes tasks with enough context. Without it, you must supply that plan yourself; the reminder to use spinoff for new branches is also removed.`
	case "nerdr-tools":
		return `CLI: nerdr status "Work is ready"
     nerdr tabs
     nerdr terminal npm run dev
     nerdr terminal --restart terminal
     nerdr read terminal
     nerdr close terminal
     nerdr tab new --branch fix/example "Complete task"
     nerdr search WORDS
     nerdr search --show ID
     nerdr usage

These replace the individual actions. Without the skill, the agent loses guidance on keeping status current, showing long runs in terminals and finding past decisions. tab new starts an agent and can use tokens; close stops the pane's work. Sandbox restrictions still apply.`
	}
	return ""
}

// CostHelp avoids equating skill-use counts with measured token savings.
const CostHelp = "Skill descriptions can take context even before use; reading instructions, tool calls and the work itself also use tokens. Counts here are uses found in agent logs, not token costs. Manual CLI use can avoid agent calls; asking an agent to read CLI help and do the same work still costs tokens. Restart agents after changing skills; existing conversation text remains."

const CLIHelp = "CLI examples: replace FILE, NAME, ID and other placeholders. Use nerdr COMMAND -h for options and input formats. Commands that control a tab need the agent's nerdr environment, which terminal panes do not have. Ask the agent to run an exact command without loading a skill. Local note commands and plugin commands can also run from a shell. Agents should use \"${NERDR_BIN:-nerdr}\" in place of nerdr."

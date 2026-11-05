package app

// What an agent can ask nerdr of the rest of nerdr, beyond its own work:
// the tab's agents and their state (`nerdr agents`, and `nerdr wait`,
// which asks again until they are done), what another pane of the tab
// shows (`nerdr read`), every tab (`nerdr tabs`), where conversations are
// searched (`nerdr search`); and what it can have nerdr do: run a command
// in a terminal pane the user sees (`nerdr terminal`), or again in one
// (`nerdr terminal --restart`), close a pane when the user asked for it
// (`nerdr close`), and make a tab on a new branch with an agent given a
// task (`nerdr tab new`).

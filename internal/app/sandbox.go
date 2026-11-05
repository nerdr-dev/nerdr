package app

// An agent whose kind is set sandboxed = true runs in a sandbox its
// command puts it in, and nerdr is not to be its way out. nerdr runs as
// the user, outside every sandbox, so what it does when an agent asks is
// done with everything the user can do. Such an agent is confined, and
// nerdr refuses it
//
//   - a terminal pane (`nerdr terminal`): the user's shell, outside the
//     sandbox;
//   - an agent, a tab or a spun-off part run by a kind that is not
//     sandboxed: it would run outside, with the task the confined agent
//     wrote;
//   - a message to an agent not in a sandbox, which could be told to run
//     what the confined one cannot;
//   - reading a terminal or an agent outside a sandbox, and the other tabs:
//     what it could not see from inside;
//   - closing a terminal or an agent outside a sandbox, and what runs in
//     it.
//
// Reviews of its work run only in sandboxed agents too. What it says for
// the user (ask, try, a review result, a handoff) is shown to the user, or
// given to the agent the user picks.
//
// It reports to the socket the others do. A tab's secret alone does not
// say which agent asks: it is in the agents' environment, where a process
// in a sandbox may read it. So nerdr asks the kernel which process
// connected (see vouch), and one in a sandbox cannot pass for an agent
// outside one.

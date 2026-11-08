package core

// Reply is what core tells the client in answer to a request of its,
// worked out away from the loop: the git and gh a request runs are core's,
// as the client may be on another machine than the repositories. The loop
// tells it (Client.Tell) once it is there.
type Reply struct{ Ev Event }

// Later runs fn away from the loop, and tells the client what it gives.
func Later(fn func() Event) Cmd {
	return func() Event { return Reply{fn()} }
}

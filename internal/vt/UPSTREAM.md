# internal/vt

A fork of `github.com/charmbracelet/x/vt`, MIT licensed (see `LICENSE`).

- Upstream: https://github.com/charmbracelet/x, directory `vt`
- Based on: `v0.0.0-20260924144451-d676b019604b` (commit `d676b019604b`)

nerdr needs the emulator's private state to take a checkpoint of a screen
and put it back, which the public API does not give.

## Changes from upstream

Every file not listed here is upstream's, byte for byte; `go.mod` and
`go.sum` are left out, since the fork is part of nerdr's module.

- `checkpoint.go` and `checkpoint_test.go` (new): `Emulator.Checkpoint`,
  `Restore`, `AtGround` and `CheckpointVersionOf`. All of the emulator's
  state, both screens and their scrollback, in a compact format of nerdr's
  own with a version. It reads private fields: a change upstream to the
  fields it reads (`Emulator`, `Screen`, `Scrollback`, `Cursor`) must be
  matched there, and `CheckpointVersion` raised if what is written changes.

- `handlers.go`, DECSTBM: a bottom margin below the screen is taken as the
  screen's bottom once it is checked to be below the top. Upstream keeps it, and IL, DL and a line
  feed in the region then index past the buffer and panic. Test in
  `margins_test.go` (new).

- `screen.go`, `DeleteLine`: a scroll over the whole width moves the
  lines instead of copying their cells, and blanks those leaving to come in
  at the bottom; `copyOnScroll` turns it off for the tests. About 7 times
  faster on scrolling output. Tests in `fastscroll_test.go` (new) compare
  it with upstream's.

## Updating

1. Copy the new upstream version over this directory, leaving out `go.mod`
   and `go.sum`, and put back the changes listed above.
2. Update "Based on" and run `go mod tidy`.
3. `diff -r` against the upstream version in the module cache must show only
   the changes listed above.
4. `go test -race ./internal/vt ./internal/term`.

# Session files of released versions

nerdr reads the session files of every version before it (see
`TestOldSessionsAreRead` in `internal/state` and
`TestACoreStartsFromOldSessions` in `internal/core`). Each version has two,
written by that version's own `state.Save`:

- `vX.Y.Z-tabs.json`: two tabs as nerdr makes them, one with an agent
  beside it and messages waiting, including the queue of old.
- `vX.Y.Z-full.json`: every field set, so a field renamed or dropped since
  is seen.

Folders are written as `/ROOT`; the tests put their own in its place.

To add a version, run `mksession` from this folder in that version's
source:

```sh
git archive vX.Y.Z | tar -x -C /tmp/nerdr-vX.Y.Z
mkdir -p /tmp/nerdr-vX.Y.Z/cmd/mksession out
cp mksession/main.go /tmp/nerdr-vX.Y.Z/cmd/mksession/
(cd /tmp/nerdr-vX.Y.Z && go run ./cmd/mksession "$OLDPWD/out")
mv out/tabs.json vX.Y.Z-tabs.json; mv out/full.json vX.Y.Z-full.json
```

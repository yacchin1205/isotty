# up specification

`isotty up` runs the steps in `upSteps`
([internal/isotty/upsteps.go](../internal/isotty/upsteps.go)). The effects
each step declares are generated into `Spec/Steps.lean`, and `lake build`
proves that one `up` that is not interrupted reaches every step, for every
configuration, whatever earlier `up`s stopped part way did. CI builds it, and
`go test` fails when `Steps.lean` is not the table.

After changing the table, run
`go test ./internal/isotty -run TestSpecSteps -update` and `lake build` here.
If Lean fails, the new order breaks what the proof needs.

The proof takes as given what it cannot check: that a step's check and
command do what its declared effects say, that what an earlier `up` left
running in the VM ends before the next `up` starts its steps, and that the
saved agent homes change only at `down`.

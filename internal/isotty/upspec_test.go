package isotty

import (
	"flag"
	"os"
	"testing"
)

var update = flag.Bool("update", false, "write spec/Spec/Steps.lean from the step table")

// TestSpecSteps keeps spec/Spec/Steps.lean, which Lean checks, the steps
// that up runs.
func TestSpecSteps(t *testing.T) {
	const path = "../../spec/Spec/Steps.lean"
	want := SpecSteps()
	if *update {
		if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("%s is not the step table of up; run `go test ./internal/isotty -run TestSpecSteps -update`", path)
	}
}

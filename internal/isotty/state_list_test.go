package isotty

import (
	"os"
	"path/filepath"
	"testing"
)

// The host keeps more of a project than its state, under names of the same
// hash; only <hash>.json is a state.
func TestListStatesReadsOnlyStates(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ISOTTY_HOME", home)
	if err := SaveState(State{ProjectPath: "/projects/a", ProjectHash: "abc"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "projects", "abc.wakes.json"), []byte("[]"), 0o600); err != nil {
		t.Fatal(err)
	}
	states, err := ListStates()
	if err != nil || len(states) != 1 || states[0].ProjectHash != "abc" {
		t.Fatalf("states %+v, err %v", states, err)
	}
}

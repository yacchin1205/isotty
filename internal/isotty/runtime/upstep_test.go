package runtimecfg

import (
	"strings"
	"testing"
)

func TestTagUpStepKeepsTheMarkAcrossSudo(t *testing.T) {
	command := TagUpStep("apt", "sudo apt-get install -y jq")
	for _, want := range []string{"export ISOTTY_UP_STEP='apt'", "--preserve-env=ISOTTY_UP_STEP", "export -f sudo", "sudo apt-get install -y jq"} {
		if !strings.Contains(command, want) {
			t.Fatalf("command missing %q: %s", want, command)
		}
	}
}

func TestParseRunningUpSteps(t *testing.T) {
	running, err := ParseRunningUpSteps("812 apt 40\n813 apt 39\n812 apt 40\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(running) != 2 || running[0] != (RunningUpStep{PID: 812, Step: "apt", Seconds: 40}) {
		t.Fatalf("running = %v", running)
	}
	if running, err := ParseRunningUpSteps("\n"); err != nil || len(running) != 0 {
		t.Fatalf("nothing running: %v, %v", running, err)
	}
	if _, err := ParseRunningUpSteps("812 apt\n"); err == nil {
		t.Fatal("a short line should fail")
	}
}

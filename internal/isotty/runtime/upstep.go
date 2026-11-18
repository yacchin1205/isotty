package runtimecfg

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

// UpStepEnv marks every process an up step starts in the VM, so that the
// next up finds what an interrupted one left running. Processes inherit it,
// and sudo is told to keep it.
const UpStepEnv = "ISOTTY_UP_STEP"

// TagUpStep runs command as the up step named step.
func TagUpStep(step, command string) string {
	return fmt.Sprintf(`export %s=%s; sudo() { command sudo --preserve-env=%s "$@"; }; export -f sudo; %s`,
		UpStepEnv, shellJoin([]string{step}), UpStepEnv, command)
}

// runningScript prints "pid step seconds" for each process that carries
// UpStepEnv. A process may end while it is read. Tagged processes run as
// the login user or, under sudo, as root, and reading another user's
// environment needs CAP_SYS_PTRACE, which a container may lack; so the
// script runs as both, and each skips what it may not read.
const runningScript = `import os
ticks = os.sysconf("SC_CLK_TCK")
with open("/proc/uptime") as f:
    uptime = float(f.read().split()[0])
for pid in os.listdir("/proc"):
    if not pid.isdigit():
        continue
    try:
        with open(f"/proc/{pid}/environ", "rb") as f:
            env = f.read().split(b"\0")
        with open(f"/proc/{pid}/stat") as f:
            start = int(f.read().rsplit(")", 1)[1].split()[19])
    except (FileNotFoundError, ProcessLookupError, PermissionError):
        continue
    for entry in env:
        if entry.startswith(b"` + UpStepEnv + `="):
            print(pid, entry.split(b"=", 1)[1].decode(), int(uptime - start / ticks))
`

// RunningUpStepsCommand lists what earlier ups left running in the VM.
func RunningUpStepsCommand() string {
	return fmt.Sprintf(`script=$(printf %%s %s | base64 -d) && python3 -c "$script" && sudo python3 -c "$script"`,
		base64.StdEncoding.EncodeToString([]byte(runningScript)))
}

// RunningUpStep is a process an earlier up left running.
type RunningUpStep struct {
	PID     int
	Step    string
	Seconds int
}

// ParseRunningUpSteps reads what RunningUpStepsCommand printed; a process
// both readers saw is listed once.
func ParseRunningUpSteps(output string) ([]RunningUpStep, error) {
	var running []RunningUpStep
	seen := map[int]bool{}
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 {
			return nil, fmt.Errorf("unexpected line from the VM: %q", line)
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			return nil, fmt.Errorf("unexpected line from the VM: %q", line)
		}
		seconds, err := strconv.Atoi(fields[2])
		if err != nil {
			return nil, fmt.Errorf("unexpected line from the VM: %q", line)
		}
		if seen[pid] {
			continue
		}
		seen[pid] = true
		running = append(running, RunningUpStep{PID: pid, Step: fields[1], Seconds: seconds})
	}
	return running, nil
}

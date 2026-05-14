package isotty

import (
	"encoding/json"
	"fmt"
	"os"
	"os/user"
)

const vmEventLogPath = "/var/log/isotty/events.jsonl"

func newAttachVMEvent(state State, eventType string, forwardCount int, result string) (VMEventRecord, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return VMEventRecord{}, fmt.Errorf("resolve client hostname: %w", err)
	}
	currentUser, err := user.Current()
	if err != nil {
		return VMEventRecord{}, fmt.Errorf("resolve client user: %w", err)
	}

	return VMEventRecord{
		Type:           eventType,
		ClientHostname: hostname,
		ClientUser:     currentUser.Username,
		ProjectHash:    state.ProjectHash,
		ForwardCount:   forwardCount,
		Result:         result,
	}, nil
}

func recordVMEvent(state State, record VMEventRecord, debug bool) error {
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal VM event: %w", err)
	}

	script := `import datetime, json, os, sys; path = sys.argv[1]; entry = json.loads(sys.argv[2]); entry["time"] = datetime.datetime.now(datetime.timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z"); os.makedirs(os.path.dirname(path), exist_ok=True); handle = open(path, "a", encoding="utf-8"); handle.write(json.dumps(entry, separators=(",", ":")) + "\n"); handle.close()`

	command := fmt.Sprintf(
		"sudo python3 -c %s %s %s",
		shellJoin([]string{script}),
		shellJoin([]string{vmEventLogPath}),
		shellJoin([]string{string(data)}),
	)
	return RunCommand("", os.Environ(), debug, "gcloud",
		"compute", "ssh", state.InstanceName,
		"--project", state.GCPProjectID,
		"--zone", state.Zone,
		"--command", command,
	)
}

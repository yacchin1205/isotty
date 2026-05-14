package isotty

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	auditEventIDPattern  = regexp.MustCompile(`audit\(.*:([0-9]+)\)`)
	auditTimePattern     = regexp.MustCompile(`audit\(([0-9]+(?:\.[0-9]+)?):[0-9]+\)`)
	auditDateTimePattern = regexp.MustCompile(`audit\(([0-9]{2}/[0-9]{2}/[0-9]{2} [0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]+)?):[0-9]+\)`)
)

const (
	auditExecKey    = "isotty-exec"
	auditConnectKey = "isotty-connect"
)

var auditPackages = []string{"auditd"}

type AuditEvent struct {
	EventID    string
	Source     string
	Kind       string
	Time       time.Time
	Executable string
	Command    string
	Address    string
	Proctitle  string
	Message    string
}

func (e AuditEvent) RawSummary() string {
	if e.Message != "" {
		return e.Message
	}
	parts := []string{e.Kind}
	if e.Command != "" {
		parts = append(parts, e.Command)
	}
	if e.Address != "" {
		parts = append(parts, e.Address)
	}
	if e.Executable != "" && e.Command == "" {
		parts = append(parts, e.Executable)
	}
	if e.Proctitle != "" && e.Command == "" && e.Executable == "" {
		parts = append(parts, e.Proctitle)
	}
	return strings.Join(parts, " ")
}

type VMEventRecord struct {
	Time           string `json:"time"`
	Type           string `json:"type"`
	ClientHostname string `json:"client_hostname,omitempty"`
	ClientUser     string `json:"client_user,omitempty"`
	ProjectHash    string `json:"project_hash,omitempty"`
	ForwardCount   int    `json:"forward_count,omitempty"`
	Result         string `json:"result,omitempty"`
}

func configureAudit(state State, debug bool) error {
	command := strings.Join([]string{
		"set -euo pipefail",
		"export DEBIAN_FRONTEND=noninteractive",
		"sudo apt-get update",
		fmt.Sprintf("sudo apt-get install -y %s", shellJoin(auditPackages)),
		"sudo systemctl enable auditd",
		fmt.Sprintf("sudo sh -c 'cat > /etc/audit/rules.d/isotty.rules <<\"EOF\"\n%s\nEOF'", auditRules()),
		"sudo augenrules --load",
		"sudo systemctl restart auditd",
	}, " && ")
	return RunCommand("", os.Environ(), debug, "gcloud",
		"compute", "ssh", state.InstanceName,
		"--project", state.GCPProjectID,
		"--zone", state.Zone,
		"--command", command,
	)
}

func auditRules() string {
	lines := []string{
		fmt.Sprintf("-a always,exit -F arch=b64 -S execve -k %s", auditExecKey),
		fmt.Sprintf("-a always,exit -F arch=b32 -S execve -k %s", auditExecKey),
		fmt.Sprintf("-a always,exit -F arch=b64 -S connect -k %s", auditConnectKey),
		fmt.Sprintf("-a always,exit -F arch=b32 -S connect -k %s", auditConnectKey),
	}
	return strings.Join(lines, "\n")
}

func queryAuditLogs(state State, start string) ([]AuditEvent, error) {
	execOutput, err := queryAuditKey(state, auditExecKey, start)
	if err != nil {
		return nil, err
	}
	connectOutput, err := queryAuditKey(state, auditConnectKey, start)
	if err != nil {
		return nil, err
	}

	execEvents, err := parseAuditEvents(execOutput, "exec")
	if err != nil {
		return nil, fmt.Errorf("parse exec audit events: %w", err)
	}
	connectEvents, err := parseAuditEvents(connectOutput, "connect")
	if err != nil {
		return nil, fmt.Errorf("parse connect audit events: %w", err)
	}

	vmEvents, err := queryVMEvents(state)
	if err != nil {
		return nil, fmt.Errorf("query isotty VM events: %w", err)
	}

	events := append(execEvents, connectEvents...)
	events = append(events, vmEvents...)
	sort.Slice(events, func(i, j int) bool {
		if events[i].Time.Equal(events[j].Time) {
			return events[i].EventID < events[j].EventID
		}
		return events[i].Time.Before(events[j].Time)
	})
	return events, nil
}

func queryAuditKey(state State, key, start string) (string, error) {
	output, err := CaptureCommand("", os.Environ(), "gcloud",
		"compute", "ssh", state.InstanceName,
		"--project", state.GCPProjectID,
		"--zone", state.Zone,
		"--command", fmt.Sprintf("sudo ausearch --input-logs -i --start %s --key %s", start, key),
	)
	if err == nil {
		return output, nil
	}
	var commandErr *CommandError
	if errors.As(err, &commandErr) {
		if ExitCode(commandErr.Err) == 1 && strings.Contains(commandErr.Stdout, "<no matches>") {
			return "", nil
		}
		if ExitCode(commandErr.Err) == 1 && strings.Contains(commandErr.Stderr, "<no matches>") {
			return "", nil
		}
	}
	return "", err
}

func queryVMEvents(state State) ([]AuditEvent, error) {
	output, err := CaptureCommand("", os.Environ(), "gcloud",
		"compute", "ssh", state.InstanceName,
		"--project", state.GCPProjectID,
		"--zone", state.Zone,
		"--command", `sudo python3 -c 'import os,sys; path=sys.argv[1]; 
if os.path.exists(path):
    with open(path, "r", encoding="utf-8") as handle:
        sys.stdout.write(handle.read())' /var/log/isotty/events.jsonl`,
	)
	if err != nil {
		return nil, err
	}
	return parseVMEvents(output)
}

func parseAuditEvents(output, kind string) ([]AuditEvent, error) {
	if strings.TrimSpace(output) == "" {
		return nil, nil
	}

	var events []AuditEvent
	blocks := strings.Split(output, "----")
	for index, block := range blocks {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		event, err := parseAuditBlock(block, kind)
		if err != nil {
			return nil, fmt.Errorf("block %d: %w", index+1, err)
		}
		events = append(events, event)
	}
	return events, nil
}

func parseVMEvents(output string) ([]AuditEvent, error) {
	if strings.TrimSpace(output) == "" {
		return nil, nil
	}

	lines := strings.Split(output, "\n")
	events := make([]AuditEvent, 0, len(lines))
	for index, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var record VMEventRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			return nil, fmt.Errorf("line %d: parse VM event JSON: %w", index+1, err)
		}
		if strings.TrimSpace(record.Time) == "" {
			return nil, fmt.Errorf("line %d: missing VM event time", index+1)
		}
		parsedTime, err := time.Parse(time.RFC3339, record.Time)
		if err != nil {
			return nil, fmt.Errorf("line %d: parse VM event time: %w", index+1, err)
		}
		if strings.TrimSpace(record.Type) == "" {
			return nil, fmt.Errorf("line %d: missing VM event type", index+1)
		}

		events = append(events, AuditEvent{
			EventID: fmt.Sprintf("vm-event-%d", index+1),
			Source:  "isotty",
			Kind:    "isotty",
			Time:    parsedTime.UTC(),
			Message: formatVMEvent(record),
		})
	}
	return events, nil
}

func parseAuditBlock(block, kind string) (AuditEvent, error) {
	lines := strings.Split(block, "\n")
	event := AuditEvent{Kind: kind, Source: "auditd"}
	var argv []string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if event.EventID == "" {
			if match := auditEventIDPattern.FindStringSubmatch(line); len(match) == 2 {
				event.EventID = match[1]
			}
		}
		if event.Time.IsZero() {
			if parsed, ok := parseAuditDisplayTime(line); ok {
				event.Time = parsed
			}
			if parsed, ok := parseAuditMessageTime(line); ok {
				event.Time = parsed
			}
			if match := auditTimePattern.FindStringSubmatch(line); len(match) == 2 {
				if ts, err := strconv.ParseFloat(match[1], 64); err == nil {
					seconds := int64(ts)
					nanos := int64((ts - float64(seconds)) * float64(time.Second))
					event.Time = time.Unix(seconds, nanos).UTC()
				}
			}
		}
		if strings.Contains(line, ` exe="`) {
			event.Executable = extractQuotedValue(line, "exe")
		}
		if event.Executable == "" && strings.Contains(line, " exe=") {
			event.Executable = extractFieldValue(line, "exe")
		}
		if strings.Contains(line, " proctitle=") || strings.HasPrefix(line, "proctitle=") {
			event.Proctitle = parseProctitle(extractFieldValue(line, "proctitle"))
		}
		if strings.HasPrefix(line, "type=EXECVE ") || strings.Contains(line, " type=EXECVE ") {
			args := extractExecArgs(line)
			if len(args) > 0 {
				argv = args
			}
		}
		if kind == "connect" {
			if address := parseSocketAddress(line); address != "" {
				event.Address = address
			}
		}
	}

	if !event.Time.IsZero() && len(argv) > 0 {
		event.Command = strings.Join(argv, " ")
	}
	if event.Time.IsZero() {
		return AuditEvent{}, fmt.Errorf("missing audit timestamp in block: %q", firstLine(block))
	}
	if event.EventID == "" {
		return AuditEvent{}, fmt.Errorf("missing audit event id in block: %q", firstLine(block))
	}
	if event.Command == "" {
		switch {
		case event.Executable != "":
			event.Command = event.Executable
		case event.Proctitle != "":
			event.Command = event.Proctitle
		case kind == "exec":
			event.Command = "[unparsed exec]"
		}
	}
	if kind == "connect" && event.Address == "" {
		switch {
		case event.Executable != "":
			event.Address = "[unparsed target] exe=" + event.Executable
		case event.Proctitle != "":
			event.Address = "[unparsed target] proctitle=" + event.Proctitle
		default:
			event.Address = "[unparsed target]"
		}
	}
	return event, nil
}

func extractQuotedValue(line, key string) string {
	pattern := key + `="`
	start := strings.Index(line, pattern)
	if start < 0 {
		return ""
	}
	start += len(pattern)
	end := strings.Index(line[start:], `"`)
	if end < 0 {
		return ""
	}
	return line[start : start+end]
}

func extractFieldValue(line, key string) string {
	pattern := key + "="
	start := strings.Index(line, pattern)
	if start < 0 {
		return ""
	}
	start += len(pattern)
	end := strings.IndexAny(line[start:], " }")
	if end < 0 {
		return line[start:]
	}
	return line[start : start+end]
}

func parseAuditDisplayTime(line string) (time.Time, bool) {
	if !strings.HasPrefix(line, "time->") {
		return time.Time{}, false
	}
	value := strings.TrimSpace(strings.TrimPrefix(line, "time->"))
	parsed, err := time.ParseInLocation("Mon Jan 2 15:04:05 2006", value, time.UTC)
	if err != nil {
		return time.Time{}, false
	}
	return parsed.UTC(), true
}

func parseAuditMessageTime(line string) (time.Time, bool) {
	match := auditDateTimePattern.FindStringSubmatch(line)
	if len(match) != 2 {
		return time.Time{}, false
	}
	layouts := []string{
		"01/02/06 15:04:05.000",
		"01/02/06 15:04:05",
	}
	for _, layout := range layouts {
		parsed, err := time.ParseInLocation(layout, match[1], time.UTC)
		if err == nil {
			return parsed.UTC(), true
		}
	}
	return time.Time{}, false
}

func parseSocketAddress(line string) string {
	if strings.Contains(line, "unknown-family(") {
		return extractAfter(line, "SADDR=")
	}

	family := extractFieldValue(line, "saddr_fam")
	if family == "" {
		family = extractFieldValue(line, "fam")
	}
	switch family {
	case "inet", "inet6":
		addr := extractFieldValue(line, "laddr")
		port := extractFieldValue(line, "lport")
		if addr == "" || port == "" {
			return ""
		}
		return addr + ":" + port
	case "local":
		path := extractFieldValue(line, "path")
		if path == "" {
			return "local:[unknown]"
		}
		return "local:" + path
	}

	if strings.Contains(line, "laddr=") {
		addr := extractFieldValue(line, "laddr")
		port := extractFieldValue(line, "lport")
		if addr != "" && port != "" {
			return addr + ":" + port
		}
	}
	return ""
}

func parseProctitle(value string) string {
	if value == "" {
		return ""
	}
	if decoded, ok := decodeHexProctitle(value); ok {
		return decoded
	}
	return value
}

func decodeHexProctitle(value string) (string, bool) {
	if len(value)%2 != 0 {
		return "", false
	}
	for _, r := range value {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return "", false
		}
	}
	decoded, err := hex.DecodeString(value)
	if err != nil {
		return "", false
	}
	parts := strings.FieldsFunc(string(decoded), func(r rune) bool {
		return r == 0
	})
	if len(parts) == 0 {
		return "", false
	}
	return strings.Join(parts, " "), true
}

func extractAfter(line, prefix string) string {
	start := strings.Index(line, prefix)
	if start < 0 {
		return ""
	}
	return strings.TrimSpace(line[start+len(prefix):])
}

func formatVMEvent(record VMEventRecord) string {
	parts := []string{record.Type}
	if record.ClientHostname != "" {
		parts = append(parts, "client="+record.ClientHostname)
	}
	if record.ClientUser != "" {
		parts = append(parts, "user="+record.ClientUser)
	}
	if record.ForwardCount > 0 {
		parts = append(parts, fmt.Sprintf("forwards=%d", record.ForwardCount))
	}
	if record.Result != "" {
		parts = append(parts, "result="+record.Result)
	}
	return strings.Join(parts, " ")
}

func firstLine(block string) string {
	lines := strings.Split(block, "\n")
	if len(lines) == 0 {
		return ""
	}
	return strings.TrimSpace(lines[0])
}

func extractExecArgs(line string) []string {
	fields := strings.Fields(line)
	args := make(map[int]string)
	for _, field := range fields {
		if !strings.HasPrefix(field, "a") {
			continue
		}
		parts := strings.SplitN(field, "=", 2)
		if len(parts) != 2 || len(parts[0]) < 2 {
			continue
		}
		index, err := strconv.Atoi(parts[0][1:])
		if err != nil {
			continue
		}
		value := strings.Trim(parts[1], `"`)
		args[index] = value
	}
	if len(args) == 0 {
		return nil
	}
	indexes := make([]int, 0, len(args))
	for index := range args {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	result := make([]string, 0, len(indexes))
	for _, index := range indexes {
		result = append(result, args[index])
	}
	return result
}

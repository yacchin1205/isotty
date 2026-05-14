package runtimecfg

import (
	"testing"
	"time"
)

func TestParseAuditEventsExec(t *testing.T) {
	output := `----
time->Wed May 14 01:02:03 2026
type=SYSCALL msg=audit(05/14/2026 01:02:03.123:4242): arch=x86_64 syscall=execve success=yes exit=0 exe="/usr/bin/bash" key="isotty-exec"
type=EXECVE msg=audit(05/14/2026 01:02:03.123:4242): argc=2 a0="bash" a1="script.sh"
`
	events, err := parseAuditEvents(output, "exec")
	if err != nil {
		t.Fatalf("parseAuditEvents returned error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}
	if events[0].Command != "bash script.sh" {
		t.Fatalf("Command = %q", events[0].Command)
	}
	if events[0].EventID != "4242" {
		t.Fatalf("EventID = %q", events[0].EventID)
	}
}

func TestParseAuditEventsConnect(t *testing.T) {
	output := `----
time->Wed May 14 01:02:03 2026
type=SYSCALL msg=audit(05/14/2026 01:02:03.123:4243): arch=x86_64 syscall=connect success=yes exit=0 exe="/usr/bin/curl" key="isotty-connect"
type=SOCKADDR msg=audit(05/14/2026 01:02:03.123:4243): saddr={ fam=inet laddr=104.16.25.35 lport=443 }
`
	events, err := parseAuditEvents(output, "connect")
	if err != nil {
		t.Fatalf("parseAuditEvents returned error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}
	if events[0].Address != "104.16.25.35:443" {
		t.Fatalf("Address = %q", events[0].Address)
	}
	if events[0].Executable != "/usr/bin/curl" {
		t.Fatalf("Executable = %q", events[0].Executable)
	}
	if events[0].Time.Equal(time.Time{}) {
		t.Fatal("Time should be set")
	}
}

func TestParseAuditEventsFailsOnMalformedBlock(t *testing.T) {
	output := `----
type=SYSCALL msg=audit(05/14/2026 01:02:03.123:4243): arch=x86_64 syscall=connect success=yes exit=0 exe="/usr/bin/curl" key="isotty-connect"
`
	_, err := parseAuditEvents(output, "connect")
	if err == nil {
		t.Fatal("parseAuditEvents should fail on malformed blocks")
	}
}

func TestParseAuditEventsInterpretedMessageTime(t *testing.T) {
	output := `----
type=PROCTITLE msg=audit(05/13/26 23:23:53.636:238) : proctitle=systemctl restart auditd
type=EXECVE msg=audit(05/13/26 23:23:53.636:238) : argc=3 a0=systemctl a1=restart a2=auditd
type=SYSCALL msg=audit(05/13/26 23:23:53.636:238) : arch=x86_64 syscall=execve success=yes exit=0 exe=/usr/bin/systemctl key=isotty-exec
`
	events, err := parseAuditEvents(output, "exec")
	if err != nil {
		t.Fatalf("parseAuditEvents returned error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}
	if events[0].EventID != "238" {
		t.Fatalf("EventID = %q", events[0].EventID)
	}
	if events[0].Command != "systemctl restart auditd" {
		t.Fatalf("Command = %q", events[0].Command)
	}
}

func TestParseAuditEventsDecodesHexProctitle(t *testing.T) {
	output := `----
type=PROCTITLE msg=audit(05/13/26 23:23:54.574:4022) : proctitle=7461696C002D6E003830002F7661722F6C6F672F61756469742F61756469742E6C6F67
type=SYSCALL msg=audit(05/13/26 23:23:54.574:4022) : arch=x86_64 syscall=execve success=yes exit=0 key=isotty-exec
`
	events, err := parseAuditEvents(output, "exec")
	if err != nil {
		t.Fatalf("parseAuditEvents returned error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}
	if events[0].Command != "tail -n 80 /var/log/audit/audit.log" {
		t.Fatalf("Command = %q", events[0].Command)
	}
}

func TestParseAuditEventsConnectLocalSocket(t *testing.T) {
	output := `----
type=PROCTITLE msg=audit(05/13/26 23:23:54.129:252) : proctitle=2F7573722F6C69622F73797374656D642F73797374656D642D6578656375746F72
type=SOCKADDR msg=audit(05/13/26 23:23:54.129:252) : saddr={ saddr_fam=local path=/run/systemd/journal/socket }
type=SYSCALL msg=audit(05/13/26 23:23:54.129:252) : arch=x86_64 syscall=connect success=yes exit=0 exe=/usr/lib/systemd/systemd-executor key=isotty-connect
`
	events, err := parseAuditEvents(output, "connect")
	if err != nil {
		t.Fatalf("parseAuditEvents returned error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}
	if events[0].Address != "local:/run/systemd/journal/socket" {
		t.Fatalf("Address = %q", events[0].Address)
	}
	if events[0].Executable != "/usr/lib/systemd/systemd-executor" {
		t.Fatalf("Executable = %q", events[0].Executable)
	}
}

func TestParseAuditEventsConnectUnknownFamily(t *testing.T) {
	output := `----
type=PROCTITLE msg=audit(05/13/26 23:23:54.491:4016) : proctitle=2F7573722F62696E2F6375726C002D49002D730068747470733A2F2F6578616D706C652E636F6D
type=SOCKADDR msg=audit(05/13/26 23:23:54.491:4016) : saddr=00000000000000000000000000000000 SADDR=unknown-family(0)
type=SYSCALL msg=audit(05/13/26 23:23:54.491:4016) : arch=x86_64 syscall=connect success=yes exit=0 exe=/usr/bin/curl key=isotty-connect
`
	events, err := parseAuditEvents(output, "connect")
	if err != nil {
		t.Fatalf("parseAuditEvents returned error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}
	if events[0].Address != "unknown-family(0)" {
		t.Fatalf("Address = %q", events[0].Address)
	}
}

func TestParseVMEvents(t *testing.T) {
	output := `{"time":"2026-05-14T02:20:00Z","type":"attach-start","client_hostname":"macbook-pro","client_user":"yazawa","forward_count":2}
{"time":"2026-05-14T02:25:00Z","type":"attach-end","client_hostname":"macbook-pro","client_user":"yazawa","result":"ok"}`
	events, err := parseVMEvents(output)
	if err != nil {
		t.Fatalf("parseVMEvents returned error: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("len(events) = %d, want 2", len(events))
	}
	if events[0].Source != "isotty" {
		t.Fatalf("Source = %q", events[0].Source)
	}
	if events[0].Message != "attach-start client=macbook-pro user=yazawa forwards=2" {
		t.Fatalf("Message = %q", events[0].Message)
	}
	if events[1].Message != "attach-end client=macbook-pro user=yazawa result=ok" {
		t.Fatalf("Message = %q", events[1].Message)
	}
}

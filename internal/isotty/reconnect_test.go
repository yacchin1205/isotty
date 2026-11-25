package isotty

import (
	"bytes"
	"errors"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	runtimecfg "github.com/yazawa/isotty/internal/isotty/runtime"
)

// session is how one interactive ssh goes: how long it runs and its exit
// status.
type session struct {
	ran  time.Duration
	exit string
}

// fakeSessions plays the sessions in order on a clock the test owns and
// returns the delays slept.
func fakeSessions(t *testing.T, sessions ...session) *[]time.Duration {
	t.Helper()
	clock := time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)
	var slept []time.Duration
	oldSSH, oldSleep, oldNow := interactiveSSH, sessionSleep, sessionNow
	t.Cleanup(func() { interactiveSSH, sessionSleep, sessionNow = oldSSH, oldSleep, oldNow })
	sessionNow = func() time.Time { return clock }
	sessionSleep = func(d time.Duration) { slept = append(slept, d); clock = clock.Add(d) }
	interactiveSSH = func(...string) error {
		if len(sessions) == 0 {
			t.Fatal("attached more times than expected")
		}
		s := sessions[0]
		sessions = sessions[1:]
		clock = clock.Add(s.ran)
		if s.exit == "0" {
			return nil
		}
		return exec.Command("sh", "-c", "exit "+s.exit).Run()
	}
	t.Cleanup(func() {
		if len(sessions) != 0 {
			t.Errorf("%d sessions were not attached", len(sessions))
		}
	})
	return &slept
}

func runFakeSession(t *testing.T) (error, string) {
	var out bytes.Buffer
	err := (&App{stdout: &out}).runSession(nil, runtimecfg.RemoteTarget{}, false)
	return err, out.String()
}

// A drop is waited out through attempts that fail at once, and the session
// that then attaches is the one whose end is returned.
func TestReconnectKeepsTryingAfterADrop(t *testing.T) {
	slept := fakeSessions(t,
		session{10 * time.Minute, "255"},
		session{time.Second, "255"},
		session{time.Second, "255"},
		session{time.Hour, "0"},
	)
	err, out := runFakeSession(t)
	if err != nil {
		t.Fatal(err)
	}
	if want := reconnectDelays[:3]; !slices.Equal(*slept, want) {
		t.Fatalf("slept %v, want %v", *slept, want)
	}
	if !strings.Contains(out, "(3/5)") {
		t.Fatalf("attempts not counted:\n%s", out)
	}
}

func TestReconnectGivesUpAfterTheLastDelay(t *testing.T) {
	sessions := []session{{10 * time.Minute, "255"}}
	for range reconnectDelays {
		sessions = append(sessions, session{time.Second, "255"})
	}
	slept := fakeSessions(t, sessions...)
	err, _ := runFakeSession(t)
	if err == nil || !strings.Contains(err.Error(), "failed 5 times") {
		t.Fatalf("gave up with %v", err)
	}
	if !slices.Equal(*slept, reconnectDelays) {
		t.Fatalf("slept %v", *slept)
	}
}

// A session attached again that runs a while and drops gets every attempt
// anew.
func TestReconnectStartsOverAfterASessionThatRan(t *testing.T) {
	sessions := []session{{10 * time.Minute, "255"}}
	for range reconnectDelays[1:] {
		sessions = append(sessions, session{time.Second, "255"})
	}
	sessions = append(sessions, session{10 * time.Minute, "255"})
	for range reconnectDelays[1:] {
		sessions = append(sessions, session{time.Second, "255"})
	}
	sessions = append(sessions, session{time.Minute, "0"})
	fakeSessions(t, sessions...)
	if err, out := runFakeSession(t); err != nil {
		t.Fatal(err, out)
	}
}

// The shell's own status is the session's end, not a failure to attach.
func TestReconnectReturnsTheShellsStatus(t *testing.T) {
	fakeSessions(t, session{10 * time.Minute, "255"}, session{time.Minute, "3"})
	err, _ := runFakeSession(t)
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 3 {
		t.Fatalf("ended with %v", err)
	}
}

// The first attach is not retried, however it ends.
func TestFirstAttachIsNotRetried(t *testing.T) {
	slept := fakeSessions(t, session{time.Second, "255"})
	if err, _ := runFakeSession(t); !sshFailed(err) {
		t.Fatalf("ended with %v", err)
	}
	if len(*slept) != 0 {
		t.Fatalf("slept %v", *slept)
	}
}

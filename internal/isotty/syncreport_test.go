package isotty

import (
	"errors"
	"os/exec"
	"testing"
	"time"
)

func TestParseSyncReport(t *testing.T) {
	staging := `[{"name":"isotty-5ef1","beta":{"connected":true,"scanned":true,"directories":1,"stagingProgress":{"path":"a.pack","receivedSize":17,"expectedSize":131,"receivedFiles":157255,"expectedFiles":166913,"totalReceivedSize":5420948660}},"paused":false,"status":"staging-beta"}]`
	report, err := parseSyncReport([]byte(staging))
	if err != nil || report != (syncReport{Status: "staging-beta", ReceivedFiles: 157255, ExpectedFiles: 166913}) {
		t.Fatalf("staging = %+v, %v", report, err)
	}
	// A rescan after the first cycle is still a finished sync.
	done := `[{"name":"isotty-5ef1","beta":{"connected":true,"scanned":true},"paused":false,"status":"scanning","successfulCycles":1}]`
	if report, err := parseSyncReport([]byte(done)); err != nil || report != (syncReport{Status: "scanning", Done: true}) {
		t.Fatalf("done = %+v, %v", report, err)
	}
	if _, err := parseSyncReport([]byte(`[]`)); err == nil {
		t.Fatal("no session parsed")
	}
}

func TestDroppedAfter(t *testing.T) {
	exit := func(code string) error { return exec.Command("sh", "-c", "exit "+code).Run() }
	wrapped := func(err error) error { return errors.Join(errors.New("interactive ssh failed"), err) }
	for _, c := range []struct {
		err  error
		ran  time.Duration
		want bool
	}{
		{wrapped(exit("255")), 4 * time.Minute, true},
		{wrapped(exit("255")), 10 * time.Second, false},
		{wrapped(exit("1")), 4 * time.Minute, false},
		{nil, 4 * time.Minute, false},
	} {
		if got := droppedAfter(c.err, c.ran, reconnectAfter); got != c.want {
			t.Errorf("droppedAfter(%v, %v) = %v", c.err, c.ran, got)
		}
	}
}

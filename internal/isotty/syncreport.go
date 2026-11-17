package isotty

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	runtimecfg "github.com/yazawa/isotty/internal/isotty/runtime"
	vmcfg "github.com/yazawa/isotty/internal/isotty/vm"
)

// syncReport is what the VM is told of the sync of /workspace; see
// runtime.SyncStatePath.
type syncReport struct {
	Status string `json:"status"`
	// Done is set once the session has finished a sync cycle.
	Done          bool   `json:"done"`
	ReceivedFiles uint64 `json:"received_files,omitempty"`
	ExpectedFiles uint64 `json:"expected_files,omitempty"`
}

// parseSyncReport reads the report out of `mutagen sync list --template
// '{{json .}}'` for one session.
func parseSyncReport(data []byte) (syncReport, error) {
	var sessions []struct {
		Status           string `json:"status"`
		SuccessfulCycles uint64 `json:"successfulCycles"`
		Beta             struct {
			StagingProgress *struct {
				ReceivedFiles uint64 `json:"receivedFiles"`
				ExpectedFiles uint64 `json:"expectedFiles"`
			} `json:"stagingProgress"`
		} `json:"beta"`
	}
	if err := json.Unmarshal(data, &sessions); err != nil {
		return syncReport{}, fmt.Errorf("parse the sync session: %w", err)
	}
	if len(sessions) != 1 {
		return syncReport{}, fmt.Errorf("expected one sync session, found %d", len(sessions))
	}
	session := sessions[0]
	report := syncReport{Status: session.Status, Done: session.SuccessfulCycles > 0}
	if progress := session.Beta.StagingProgress; progress != nil {
		report.ReceivedFiles = progress.ReceivedFiles
		report.ExpectedFiles = progress.ExpectedFiles
	}
	return report, nil
}

func writeSyncReport(conn vmcfg.GCPConnection, report syncReport) error {
	data, err := json.Marshal(report)
	if err != nil {
		return err
	}
	return vmcfg.RunGCPRemoteCommandWithInput(conn, runtimecfg.SyncStateCommand(), bytes.NewReader(data))
}

// reportSync tells the VM how the sync stands, every interval, until the
// session has finished a sync cycle or stop is closed. A round that fails
// is tried again at the next; the failures are reported when it ends.
func reportSync(state State, conn vmcfg.GCPConnection, stop <-chan struct{}) error {
	ticker := time.NewTicker(runtimecfg.SyncInterval)
	defer ticker.Stop()
	failures := 0
	var last error
	for {
		done, err := reportSyncOnce(state, conn)
		if err != nil {
			failures++
			last = err
		} else if done {
			break
		}
		select {
		case <-stop:
			return syncReportError(failures, last)
		case <-ticker.C:
		}
	}
	return syncReportError(failures, last)
}

func reportSyncOnce(state State, conn vmcfg.GCPConnection) (bool, error) {
	data, err := captureMutagenCommand(state.MutagenEnv(), "sync", "list", "--template", "{{json .}}", state.SessionName)
	if err != nil {
		return false, err
	}
	report, err := parseSyncReport([]byte(data))
	if err != nil {
		return false, err
	}
	return report.Done, writeSyncReport(conn, report)
}

func syncReportError(failures int, last error) error {
	if failures == 0 {
		return nil
	}
	return fmt.Errorf("telling the VM how the sync stands failed %d times; the last: %w", failures, last)
}

// Package gcloud reads what the host's gcloud login lends to the relays.
package gcloud

import (
	"fmt"
	"os/exec"
	"strings"
)

// AccessToken returns an access token of the host's active gcloud account.
// gcloud renews it as needed; a token lasts an hour.
func AccessToken() (string, error) {
	out, err := exec.Command("gcloud", "auth", "print-access-token").Output()
	if err != nil {
		var stderr string
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr = strings.TrimSpace(string(exitErr.Stderr))
		}
		return "", fmt.Errorf("gcloud auth print-access-token: %w %s", err, stderr)
	}
	return strings.TrimSpace(string(out)), nil
}

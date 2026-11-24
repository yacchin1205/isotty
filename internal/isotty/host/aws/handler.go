package aws

import (
	"errors"
	"net/http"
	"time"

	"github.com/yazawa/isotty/internal/isotty/host/gateway"
)

// IssueEvent is what the VM event log records for credentials the gateway
// obtained by assuming the role.
type IssueEvent struct {
	RoleARN    string
	Session    string
	Expiration time.Time
}

// Handler serves /status and /credentials; the latter answers as a container
// credentials endpoint, which the AWS CLI and SDKs read.
func Handler(issuer *Issuer, onIssue func(IssueEvent) error) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) {
		gateway.WriteJSON(w, http.StatusOK, map[string]any{"role_arn": issuer.cfg.RoleARN, "account": issuer.cfg.Account(), "session": issuer.session})
	})
	mux.HandleFunc("GET /credentials", func(w http.ResponseWriter, _ *http.Request) {
		creds, err := issuer.Issue(func(c Credentials) error {
			return onIssue(IssueEvent{RoleARN: c.RoleArn, Session: issuer.session, Expiration: c.Expiration})
		})
		var notRecorded RecordError
		switch {
		case errors.As(err, &notRecorded):
			gateway.WriteError(w, http.StatusInternalServerError, err.Error())
		case err != nil:
			gateway.WriteError(w, http.StatusBadGateway, "assuming the role failed: "+err.Error())
		default:
			gateway.WriteJSON(w, http.StatusOK, creds)
		}
	})
	return mux
}

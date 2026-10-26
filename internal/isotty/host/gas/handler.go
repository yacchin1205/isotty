package gas

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/yazawa/isotty/internal/isotty/host/gateway"
)

const (
	apiUpstream = "https://script.googleapis.com"
	// Apps Script lets one run last six minutes; the relay waits a little
	// longer than that for the answer.
	runTimeout = 7 * time.Minute
	maxRequest = 1 << 20
	maxResult  = 16 << 20
)

// RunEvent describes a function the relay asked Apps Script to run, and how
// it ended, so the caller can record it in the VM audit log.
type RunEvent struct {
	Account    string
	Script     string
	Function   string
	Parameters string
	Result     string
}

// Runner calls scripts.run with the host credential of each script's client.
type Runner struct {
	cfg      ProjectConfig
	tokens   map[string]*Refresher
	client   *http.Client
	upstream string
}

func NewRunner(cfg ProjectConfig, creds map[string]Credential) *Runner {
	return newRunner(cfg, creds, apiUpstream)
}

func newRunner(cfg ProjectConfig, creds map[string]Credential, upstream string) *Runner {
	tokens := map[string]*Refresher{}
	for clientID, cred := range creds {
		tokens[clientID] = NewRefresher(cred)
	}
	return &Runner{cfg: cfg, tokens: tokens, client: &http.Client{Timeout: runTimeout}, upstream: upstream}
}

// Handler serves the VM; the host gateway mounts it under /gas.
func Handler(runner *Runner, onRun func(RunEvent) error) http.Handler {
	cfg := runner.cfg
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) {
		gateway.WriteJSON(w, http.StatusOK, map[string]any{"scripts": cfg.Scripts})
	})
	mux.HandleFunc("POST /run", func(w http.ResponseWriter, req *http.Request) {
		var call struct {
			Script     string          `json:"script"`
			Function   string          `json:"function"`
			Parameters json.RawMessage `json:"parameters"`
		}
		decoder := json.NewDecoder(io.LimitReader(req.Body, maxRequest))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&call); err != nil {
			gateway.WriteError(w, http.StatusBadRequest, "the run request must be a JSON object with script, function and parameters: "+err.Error())
			return
		}
		script, ok := cfg.Find(call.Script)
		if !ok {
			var ids []string
			for _, s := range cfg.Scripts {
				ids = append(ids, s.ID)
			}
			gateway.WriteError(w, http.StatusForbidden, fmt.Sprintf("script %q is not pinned for this project; the VM may run functions of: %s", call.Script, strings.Join(ids, ", ")))
			return
		}
		if call.Function == "" {
			gateway.WriteError(w, http.StatusBadRequest, "the run request names no function")
			return
		}
		parameters, err := compactArray(call.Parameters)
		if err != nil {
			gateway.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}

		status, body, runErr := runner.run(script, call.Function, parameters)
		event := RunEvent{Account: script.Account, Script: script.ID, Function: call.Function, Parameters: string(parameters), Result: describe(status, body, runErr)}
		// The script may have run whatever the answer was, so the record is
		// made before the VM hears the outcome and a lost record is loud.
		if err := onRun(event); err != nil {
			gateway.WriteJSON(w, http.StatusInternalServerError, map[string]any{
				"error":  fmt.Sprintf("%s was run (%s) but recording the audit event failed: %v", call.Function, event.Result, err),
				"result": rawOrNull(body),
			})
			return
		}
		if runErr != nil {
			gateway.WriteError(w, http.StatusBadGateway, "Apps Script API request failed: "+runErr.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write(body)
	})
	return mux
}

// run posts the call and returns the upstream status and body; a body is
// returned only when the upstream answered.
func (r *Runner) run(script Script, function string, parameters json.RawMessage) (int, []byte, error) {
	token, err := r.tokens[script.Client].Access()
	if err != nil {
		return 0, nil, err
	}
	// devMode runs the latest saved code, which is what the human edits;
	// Google grants it to the script's owner only.
	payload, _ := json.Marshal(map[string]any{"function": function, "parameters": parameters, "devMode": true})
	req, err := http.NewRequest(http.MethodPost, r.upstream+"/v1/scripts/"+script.ID+":run", bytes.NewReader(payload))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResult))
	if err != nil {
		return 0, nil, fmt.Errorf("read response: %w", err)
	}
	return resp.StatusCode, body, nil
}

// answer is the shape of a scripts.run response: an HTTP error carries
// error.message, a script that ran but failed carries error.details.
type answer struct {
	Done  bool `json:"done"`
	Error *struct {
		Message string `json:"message"`
		Details []struct {
			ErrorMessage string `json:"errorMessage"`
			ErrorType    string `json:"errorType"`
		} `json:"details"`
	} `json:"error"`
}

func (a answer) detail() (errorType, message string) {
	if a.Error == nil {
		return "", ""
	}
	if len(a.Error.Details) > 0 && a.Error.Details[0].ErrorMessage != "" {
		return a.Error.Details[0].ErrorType, a.Error.Details[0].ErrorMessage
	}
	return "", a.Error.Message
}

// describe summarizes the outcome for the audit record: done, the script's
// own error, the API's refusal, or no answer at all.
func describe(status int, body []byte, runErr error) string {
	if runErr != nil {
		return "no response: " + runErr.Error()
	}
	var parsed answer
	if err := json.Unmarshal(body, &parsed); err != nil {
		return fmt.Sprintf("http %d: unparsable response", status)
	}
	_, message := parsed.detail()
	switch {
	case status != http.StatusOK && parsed.Error != nil:
		return fmt.Sprintf("http %d: %s", status, parsed.Error.Message)
	case status != http.StatusOK:
		return fmt.Sprintf("http %d", status)
	case parsed.Error != nil:
		return "error: " + message
	case parsed.Done:
		return "done"
	default:
		return "http 200: not done"
	}
}

// Verify asks the API to run a function the script cannot have. The API
// looks the function up only after it has loaded the script with the
// caller's token, so "function not found" proves the whole path: client
// and script in one Cloud project, scopes, ownership, and the script being
// reachable through the API. Nothing runs.
func (r *Runner) Verify(script Script) error {
	probe := "isottyProbe" + randomToken()[:8]
	status, body, err := r.run(script, probe, json.RawMessage("[]"))
	if err != nil {
		return fmt.Errorf("script %s: Apps Script API request failed: %w", script.ID, err)
	}
	var parsed answer
	if err := json.Unmarshal(body, &parsed); err != nil {
		return fmt.Errorf("script %s: Apps Script API answered %d with an unparsable body", script.ID, status)
	}
	errorType, message := parsed.detail()
	switch {
	case status == http.StatusForbidden:
		return fmt.Errorf("script %s: Apps Script API answered 403: %s. The OAuth client must belong to the Cloud project the script is attached to", script.ID, message)
	case status != http.StatusOK:
		return fmt.Errorf("script %s: Apps Script API answered %d: %s", script.ID, status, message)
	case errorType == "FUNCTION_NOT_FOUND":
		return nil
	case strings.Contains(message, "reading from storage") && strings.Contains(message, "NOT_FOUND"):
		return fmt.Errorf("script %s cannot be loaded through the API (%s). A script that was never deployed as \"API executable\" answers this way: in the editor, Deploy > New deployment > type \"API executable\", then add the script again", script.ID, message)
	case parsed.Error != nil:
		return fmt.Errorf("script %s: %s", script.ID, message)
	default:
		return nil
	}
}

// compactArray normalizes the parameters: absent means none, present means
// a JSON array, as scripts.run requires.
func compactArray(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return json.RawMessage("[]"), nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("parameters must be a JSON array: %v", err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return nil, err
	}
	return compact.Bytes(), nil
}

func rawOrNull(body []byte) json.RawMessage {
	if json.Valid(body) {
		return body
	}
	return json.RawMessage("null")
}

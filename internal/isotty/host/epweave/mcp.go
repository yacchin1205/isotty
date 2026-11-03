package epweave

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/yazawa/isotty/internal/isotty/host/gateway"
)

// mcpWriteTools are the ep_ai_mcp tools that change a pad. The relay writes
// them as the project's author, only under the title prefix, and records
// them in the audit log.
var mcpWriteTools = map[string]bool{"edit_pad": true, "create_pad": true, "send_chat_message": true}

// hiddenTools are not offered to the VM: they list every pad by id and
// know nothing about titles or the prefix, while `isotty-notes search`
// does; an agent that sees them uses them.
var hiddenTools = map[string]bool{"list_pads": true, "search_pads": true}

const waitTool = "wait_for_edits"

// waitPollInterval paces the revision checks of wait_for_edits. Tests shorten it.
var waitPollInterval = 5 * time.Second

const (
	defaultSettleSeconds  = 180
	defaultMaxWaitSeconds = 1800
	// maxWaitLimit keeps a wait inside the tool timeout the Codex setup
	// configures; Claude Code's default is far longer.
	maxWaitLimit = 3300
)

// waitToolDefinition is what tools/list offers for the gateway's own tool.
var waitToolDefinition = map[string]any{
	"name": waitTool,
	"description": "Wait for someone to edit a pad, then return once the pad has been quiet for " +
		"settleSeconds. Returns the lines added since sinceRev and their authors; timedOut is " +
		"true when nothing settled within maxWaitSeconds.",
	"inputSchema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"padId":          map[string]any{"type": "string"},
			"sinceRev":       map[string]any{"type": "integer", "description": "Revision to compare against; defaults to the current one"},
			"settleSeconds":  map[string]any{"type": "integer", "description": "Quiet time after the last edit before returning (default 180)"},
			"maxWaitSeconds": map[string]any{"type": "integer", "description": "Give up after this long (default 1800, at most 3300)"},
		},
		"required": []string{"padId"},
	},
}

// relayedHeaders travel between the VM and ep_ai_mcp unchanged; the
// Authorization header never does, the gateway sets its own.
var relayedHeaders = []string{"Content-Type", "Accept", "Mcp-Session-Id", "Mcp-Protocol-Version"}

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type toolCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// mcpRelay forwards the VM's MCP traffic to ep_ai_mcp on the ep_weave
// server with the host token.
type mcpRelay struct {
	notes   *Notes
	onWrite func(WriteEvent) error
}

func (m *mcpRelay) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet, http.MethodDelete:
		m.forward(w, r, nil)
	case http.MethodPost:
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBody))
		if err != nil {
			gateway.WriteError(w, http.StatusRequestEntityTooLarge, err.Error())
			return
		}
		m.post(w, r, body)
	default:
		gateway.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// post inspects a JSON-RPC message before forwarding it. Anything the
// gateway has no say in, including bodies ep_ai_mcp will reject itself,
// passes through unchanged.
func (m *mcpRelay) post(w http.ResponseWriter, r *http.Request, body []byte) {
	var msg rpcMessage
	if json.Unmarshal(body, &msg) != nil {
		m.forward(w, r, body)
		return
	}
	switch msg.Method {
	case "tools/list":
		status, header, result := m.send(r, body)
		if status == http.StatusOK {
			if rewritten, ok := offeredTools(result, header.Get("Content-Type"), msg.ID); ok {
				gateway.WriteJSON(w, http.StatusOK, rewritten)
				return
			}
		}
		reply(w, status, header, result)
	case "tools/call":
		var call toolCall
		if json.Unmarshal(msg.Params, &call) != nil {
			m.forward(w, r, body)
			return
		}
		if call.Arguments == nil {
			call.Arguments = map[string]any{}
		}
		switch {
		case hiddenTools[call.Name]:
			toolError(w, msg.ID, fmt.Sprintf("%s is not available here; `isotty-notes search 'title:%s*'` lists the pads under this project's prefix, and `isotty-notes search` finds any pad by title", call.Name, m.notes.prefix))
		case call.Name == waitTool:
			m.wait(w, msg.ID, call.Arguments)
		case mcpWriteTools[call.Name]:
			m.write(w, r, msg, call)
		default:
			m.forward(w, r, body)
		}
	default:
		m.forward(w, r, body)
	}
}

// write sends a pad-changing call as the project's author, after the
// prefix check, and records it.
func (m *mcpRelay) write(w http.ResponseWriter, r *http.Request, msg rpcMessage, call toolCall) {
	title, padID, err := m.writeTarget(call)
	if err != nil {
		toolError(w, msg.ID, err.Error())
		return
	}
	call.Arguments["authorMapper"] = m.notes.authorMapper
	call.Arguments["authorName"] = m.notes.authorName
	params, err := json.Marshal(call)
	if err != nil {
		toolError(w, msg.ID, err.Error())
		return
	}
	msg.Params = params
	body, err := json.Marshal(msg)
	if err != nil {
		toolError(w, msg.ID, err.Error())
		return
	}
	status, header, result := m.send(r, body)
	if status == http.StatusOK && succeeded(result, header.Get("Content-Type")) {
		if err := m.onWrite(WriteEvent{Operation: call.Name, Title: title, PadID: padID}); err != nil {
			toolError(w, msg.ID, fmt.Sprintf("the pad %s was written but recording the audit event failed: %v", padID, err))
			return
		}
	}
	reply(w, status, header, result)
}

// offeredTools is ep_ai_mcp's tool list without the hidden tools and with
// the gateway's own.
func offeredTools(body []byte, contentType string, id json.RawMessage) (map[string]any, bool) {
	var answer struct {
		Result struct {
			Tools []map[string]any `json:"tools"`
		} `json:"result"`
	}
	if json.Unmarshal(lastMessage(body, contentType), &answer) != nil || answer.Result.Tools == nil {
		return nil, false
	}
	tools := []map[string]any{}
	for _, tool := range answer.Result.Tools {
		if name, _ := tool["name"].(string); !hiddenTools[name] {
			tools = append(tools, tool)
		}
	}
	tools = append(tools, waitToolDefinition)
	return map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"tools": tools}}, true
}

// wait answers wait_for_edits: it watches the pad's head revision until a
// change has been followed by settleSeconds of quiet, or maxWaitSeconds
// pass, and returns what changed since sinceRev.
func (m *mcpRelay) wait(w http.ResponseWriter, id json.RawMessage, args map[string]any) {
	padID, _ := args["padId"].(string)
	if padID == "" {
		toolError(w, id, "padId is required")
		return
	}
	head, err := m.notes.client.RevisionsCount(padID)
	if err != nil {
		toolError(w, id, err.Error())
		return
	}
	since, err := intArg(args, "sinceRev", head)
	if err == nil && since > head {
		err = fmt.Errorf("sinceRev %d is past the current revision %d", since, head)
	}
	settle, err2 := intArg(args, "settleSeconds", defaultSettleSeconds)
	maxWait, err3 := intArg(args, "maxWaitSeconds", defaultMaxWaitSeconds)
	if err = errors.Join(err, err2, err3); err != nil {
		toolError(w, id, err.Error())
		return
	}
	maxWait = min(maxWait, maxWaitLimit)

	start := time.Now()
	var lastChange time.Time
	if since < head {
		lastChange = start
	}
	timedOut := false
	for {
		if !lastChange.IsZero() && time.Since(lastChange) >= time.Duration(settle)*time.Second {
			break
		}
		if time.Since(start) >= time.Duration(maxWait)*time.Second {
			timedOut = true
			break
		}
		time.Sleep(waitPollInterval)
		rev, err := m.notes.client.RevisionsCount(padID)
		if err != nil {
			toolError(w, id, err.Error())
			return
		}
		if rev != head {
			head = rev
			lastChange = time.Now()
		}
	}
	changes, err := m.notes.changesSince(padID, since, false)
	if err != nil {
		toolError(w, id, err.Error())
		return
	}
	text, err := json.MarshalIndent(struct {
		ChangesResult
		TimedOut bool `json:"timed_out"`
	}{changes, timedOut}, "", "  ")
	if err != nil {
		toolError(w, id, err.Error())
		return
	}
	toolResult(w, id, string(text), false)
}

// intArg reads an optional non-negative integer argument.
func intArg(args map[string]any, name string, fallback int) (int, error) {
	raw, present := args[name]
	if !present {
		return fallback, nil
	}
	value, ok := raw.(float64)
	if !ok || value != float64(int(value)) || value < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer", name)
	}
	return int(value), nil
}

// writeTarget names the pad a writing call touches and checks that it is
// the project's to write. A new pad gets an id of the shape ep_weave uses
// when the caller gave none; its title is the first line of its text.
func (m *mcpRelay) writeTarget(call toolCall) (title, padID string, err error) {
	padID, _ = call.Arguments["padId"].(string)
	if call.Name == "create_pad" {
		if padID == "" {
			padID = newPadID()
			call.Arguments["padId"] = padID
		}
		text, _ := call.Arguments["text"].(string)
		title = titleOf(text)
	} else {
		if padID == "" {
			return "", "", errors.New("padId is required")
		}
		text, err := m.notes.client.Text(padID)
		if err != nil {
			return "", "", err
		}
		title = titleOf(text)
	}
	if err := m.notes.checkPrefix(title); err != nil {
		return "", "", err
	}
	return title, padID, nil
}

// titleOf mirrors ep_weave's extractTitle: the first line of the text,
// without the marker Etherpad puts in front of a line that carries
// attributes such as a heading.
func titleOf(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	return strings.TrimLeft(line, "*")
}

func (m *mcpRelay) forward(w http.ResponseWriter, r *http.Request, body []byte) {
	status, header, result := m.send(r, body)
	reply(w, status, header, result)
}

// send relays the request to ep_ai_mcp, once more with a fresh token when
// the current one has expired, and returns what came back.
func (m *mcpRelay) send(r *http.Request, body []byte) (int, http.Header, []byte) {
	c := m.notes.client
	resp, result, err := c.retryUnauthorized(func() (*http.Response, []byte, error) {
		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		req, err := http.NewRequest(r.Method, c.URL+"/mcp", reader)
		if err != nil {
			return nil, nil, err
		}
		for _, name := range relayedHeaders {
			if value := r.Header.Get(name); value != "" {
				req.Header.Set(name, value)
			}
		}
		req.Header.Set("Authorization", "Bearer "+c.currentToken())
		return c.send(req)
	})
	if err != nil {
		return http.StatusBadGateway, http.Header{"Content-Type": {"application/json"}},
			[]byte(fmt.Sprintf(`{"error":%q}`, "ep_weave request failed: "+err.Error()))
	}
	return resp.StatusCode, resp.Header, result
}

func reply(w http.ResponseWriter, status int, header http.Header, body []byte) {
	for _, name := range relayedHeaders {
		if value := header.Get(name); value != "" {
			w.Header().Set(name, value)
		}
	}
	w.WriteHeader(status)
	w.Write(body)
}

// lastMessage is the JSON-RPC message of an answer; ep_ai_mcp answers in
// JSON or as SSE, where the last data line is the response.
func lastMessage(body []byte, contentType string) []byte {
	if !strings.HasPrefix(contentType, "text/event-stream") {
		return body
	}
	var raw []byte
	for _, line := range strings.Split(string(body), "\n") {
		if data, ok := strings.CutPrefix(line, "data:"); ok {
			raw = []byte(strings.TrimSpace(data))
		}
	}
	return raw
}

// succeeded reports whether a tools/call answer carries a result that is
// not a tool error.
func succeeded(body []byte, contentType string) bool {
	raw := lastMessage(body, contentType)
	var answer struct {
		Result *struct {
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	return json.Unmarshal(raw, &answer) == nil && answer.Result != nil && !answer.Result.IsError
}

// toolError answers the call the way ep_ai_mcp reports a failed tool, so
// the agent sees the reason as the tool's output.
func toolError(w http.ResponseWriter, id json.RawMessage, text string) {
	toolResult(w, id, text, true)
}

func toolResult(w http.ResponseWriter, id json.RawMessage, text string, isError bool) {
	if id == nil {
		id = json.RawMessage("null")
	}
	gateway.WriteJSON(w, http.StatusOK, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result": map[string]any{
			"content": []map[string]string{{"type": "text", "text": text}},
			"isError": isError,
		},
	})
}

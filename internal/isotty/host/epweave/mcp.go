package epweave

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/yazawa/isotty/internal/isotty/host/gateway"
)

// mcpWriteTools are the ep_ai_mcp tools that change a pad. The relay writes
// them as the project's author, only under the title prefix, and records
// them in the audit log.
var mcpWriteTools = map[string]bool{"edit_pad": true, "create_pad": true, "send_chat_message": true}

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

// post inspects a JSON-RPC message before forwarding it. Anything that is
// not a tools/call of a writing tool, including bodies ep_ai_mcp will
// reject itself, passes through unchanged.
func (m *mcpRelay) post(w http.ResponseWriter, r *http.Request, body []byte) {
	var msg rpcMessage
	var call toolCall
	if json.Unmarshal(body, &msg) != nil || msg.Method != "tools/call" ||
		json.Unmarshal(msg.Params, &call) != nil || !mcpWriteTools[call.Name] {
		m.forward(w, r, body)
		return
	}
	if call.Arguments == nil {
		call.Arguments = map[string]any{}
	}
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
	body, err = json.Marshal(msg)
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

// succeeded reports whether a tools/call answer carries a result that is
// not a tool error; ep_ai_mcp answers in JSON or as one SSE message.
func succeeded(body []byte, contentType string) bool {
	raw := body
	if strings.HasPrefix(contentType, "text/event-stream") {
		raw = nil
		for _, line := range strings.Split(string(body), "\n") {
			if data, ok := strings.CutPrefix(line, "data:"); ok {
				raw = []byte(strings.TrimSpace(data))
			}
		}
	}
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
	if id == nil {
		id = json.RawMessage("null")
	}
	gateway.WriteJSON(w, http.StatusOK, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result": map[string]any{
			"content": []map[string]string{{"type": "text", "text": text}},
			"isError": true,
		},
	})
}

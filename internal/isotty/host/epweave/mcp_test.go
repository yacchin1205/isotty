package epweave

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"
)

// mcpCall sends one tools/call through the gateway and returns the status,
// the content type and the decoded answer, whether it came as SSE or JSON.
func mcpCall(t *testing.T, gatewayURL, name string, args map[string]any) (int, string, map[string]any) {
	t.Helper()
	return mcpPost(t, gatewayURL, map[string]any{
		"jsonrpc": "2.0", "id": 7, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": args},
	})
}

func mcpPost(t *testing.T, gatewayURL string, message map[string]any) (int, string, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(message)
	req, _ := http.NewRequest(http.MethodPost, gatewayURL+"/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	data := raw
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		for _, line := range strings.Split(string(raw), "\n") {
			if d, ok := strings.CutPrefix(line, "data:"); ok {
				data = []byte(strings.TrimSpace(d))
			}
		}
	}
	var answer map[string]any
	if err := json.Unmarshal(data, &answer); err != nil {
		t.Fatalf("answer %q: %v", raw, err)
	}
	return resp.StatusCode, resp.Header.Get("Content-Type"), answer
}

func toolText(t *testing.T, answer map[string]any) (string, bool) {
	t.Helper()
	result, ok := answer["result"].(map[string]any)
	if !ok {
		t.Fatalf("answer has no result: %v", answer)
	}
	content := result["content"].([]any)[0].(map[string]any)
	isError, _ := result["isError"].(bool)
	return content["text"].(string), isError
}

func TestMCPRelayWritesAsTheProjectAuthor(t *testing.T) {
	gateway, etherpad, _, events := newTestGateway(t)
	etherpad.pads["pad1"] = &fakePad{html: "<!DOCTYPE HTML><html><body><h1>isotty/notes</h1>first<br></body></html>"}
	etherpad.expireToken()

	status, contentType, answer := mcpCall(t, gateway.URL, "edit_pad", map[string]any{
		"padId": "pad1", "action": "append", "text": "\nsecond", "authorName": "whatever the agent says",
	})
	if text, isError := toolText(t, answer); status != http.StatusOK || isError || text != "Edit applied" {
		t.Fatalf("status = %d, answer = %v", status, answer)
	}
	if !strings.HasPrefix(contentType, "text/event-stream") {
		t.Fatalf("content type = %q, want the SSE answer relayed as is", contentType)
	}
	call := etherpad.mcpCalls[len(etherpad.mcpCalls)-1]
	if call.Arguments["authorMapper"] != "isotty-abc" || call.Arguments["authorName"] != "isotty:isotty" {
		t.Fatalf("arguments = %v, want the project's author", call.Arguments)
	}
	if got := plainText(etherpad.pads["pad1"].html); got != "isotty/notes\nfirst\nsecond\n" {
		t.Fatalf("pad text = %q", got)
	}
	if len(*events) != 1 || (*events)[0] != (WriteEvent{Operation: "edit_pad", Title: "isotty/notes", PadID: "pad1"}) {
		t.Fatalf("events = %v", *events)
	}
}

func TestMCPRelayRefusesWritesOutsideThePrefix(t *testing.T) {
	gateway, etherpad, _, events := newTestGateway(t)
	etherpad.pads["theirs"] = &fakePad{html: "<!DOCTYPE HTML><html><body>other/notes<br>first<br></body></html>"}

	for _, call := range []struct {
		name string
		args map[string]any
	}{
		{"edit_pad", map[string]any{"padId": "theirs", "action": "append", "text": "\nx"}},
		{"send_chat_message", map[string]any{"padId": "theirs", "message": "hi"}},
		{"create_pad", map[string]any{"text": "other/new\nbody\n"}},
		{"edit_pad", map[string]any{"action": "append", "text": "\nx"}},
	} {
		status, _, answer := mcpCall(t, gateway.URL, call.name, call.args)
		text, isError := toolText(t, answer)
		if status != http.StatusOK || !isError {
			t.Fatalf("%s: status = %d, answer = %v", call.name, status, answer)
		}
		if _, hasPad := call.args["padId"]; !hasPad && call.name == "edit_pad" {
			if text != "padId is required" {
				t.Fatalf("%s without padId: %q", call.name, text)
			}
		} else if !strings.Contains(text, `outside the writable prefix "isotty/"`) {
			t.Fatalf("%s: %q", call.name, text)
		}
	}
	if len(etherpad.mcpCalls) != 0 || len(*events) != 0 {
		t.Fatalf("ep_ai_mcp saw %v and the audit log %v; refused writes must reach neither", etherpad.mcpCalls, *events)
	}
}

func TestMCPRelayCreatePadGetsAnIDAndTheProjectAuthor(t *testing.T) {
	gateway, etherpad, _, events := newTestGateway(t)

	status, _, answer := mcpCall(t, gateway.URL, "create_pad", map[string]any{"text": "isotty/plan\nstep one\n"})
	if text, isError := toolText(t, answer); status != http.StatusOK || isError || !strings.Contains(text, `"created": true`) {
		t.Fatalf("status = %d, answer = %v", status, answer)
	}
	call := etherpad.mcpCalls[0]
	padID, _ := call.Arguments["padId"].(string)
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(padID) {
		t.Fatalf("padId = %q, want a UUID like the pads ep_weave creates", padID)
	}
	if call.Arguments["authorMapper"] != "isotty-abc" {
		t.Fatalf("arguments = %v", call.Arguments)
	}
	if got := plainText(etherpad.pads[padID].html); got != "isotty/plan\nstep one\n" {
		t.Fatalf("pad text = %q", got)
	}
	if len(*events) != 1 || (*events)[0] != (WriteEvent{Operation: "create_pad", Title: "isotty/plan", PadID: padID}) {
		t.Fatalf("events = %v", *events)
	}
}

func TestMCPRelayPassesReadsAndSessionsThrough(t *testing.T) {
	gateway, etherpad, _, events := newTestGateway(t)
	etherpad.pads["theirs"] = &fakePad{html: "<!DOCTYPE HTML><html><body>other/notes<br>first<br></body></html>"}

	status, _, answer := mcpPost(t, gateway.URL, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "t", "version": "0"}},
	})
	if status != http.StatusOK || answer["result"].(map[string]any)["serverInfo"].(map[string]any)["name"] != "etherpad" {
		t.Fatalf("initialize: status = %d, answer = %v", status, answer)
	}

	status, _, answer = mcpCall(t, gateway.URL, "get_pad_text", map[string]any{"padId": "theirs"})
	if text, isError := toolText(t, answer); status != http.StatusOK || isError || text != "other/notes\nfirst\n" {
		t.Fatalf("get_pad_text: status = %d, answer = %v", status, answer)
	}
	if call := etherpad.mcpCalls[0]; len(call.Arguments) != 1 {
		t.Fatalf("read arguments = %v, want them untouched", call.Arguments)
	}
	if len(*events) != 0 {
		t.Fatalf("events = %v, reads are not recorded", *events)
	}

	req, _ := http.NewRequest(http.MethodGet, gateway.URL+"/mcp", nil)
	req.Header.Set("Mcp-Session-Id", "session-1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("GET status = %d, want ep_ai_mcp's own answer relayed", resp.StatusCode)
	}
}

func TestMCPRelayReportsAFailedAuditRecord(t *testing.T) {
	gateway, etherpad, _, _ := newTestGateway(t)
	etherpad.pads["pad1"] = &fakePad{html: "<!DOCTYPE HTML><html><body>isotty/notes<br>first<br></body></html>"}
	failRecording = true
	t.Cleanup(func() { failRecording = false })

	status, _, answer := mcpCall(t, gateway.URL, "edit_pad", map[string]any{"padId": "pad1", "action": "append", "text": "\nsecond"})
	text, isError := toolText(t, answer)
	if status != http.StatusOK || !isError || !strings.Contains(text, "the pad pad1 was written but recording the audit event failed") {
		t.Fatalf("status = %d, answer = %v", status, answer)
	}
	if got := plainText(etherpad.pads["pad1"].html); got != "isotty/notes\nfirst\nsecond\n" {
		t.Fatalf("pad text = %q, the write itself must stand", got)
	}
}

func TestMCPRelayHidesTheListingTools(t *testing.T) {
	gateway, etherpad, _, _ := newTestGateway(t)

	status, _, answer := mcpPost(t, gateway.URL, map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/list"})
	if status != http.StatusOK {
		t.Fatalf("tools/list status = %d, answer = %v", status, answer)
	}
	var names []string
	for _, tool := range answer["result"].(map[string]any)["tools"].([]any) {
		names = append(names, tool.(map[string]any)["name"].(string))
	}
	if strings.Join(names, ",") != "edit_pad,wait_for_edits" {
		t.Fatalf("tools = %v, want the listing tools hidden and wait_for_edits added", names)
	}

	status, _, answer = mcpCall(t, gateway.URL, "list_pads", map[string]any{})
	text, isError := toolText(t, answer)
	if status != http.StatusOK || !isError || !strings.Contains(text, "isotty-notes search 'title:isotty/*'") {
		t.Fatalf("list_pads: status = %d, answer = %v", status, answer)
	}
	if len(etherpad.mcpCalls) != 0 {
		t.Fatalf("ep_ai_mcp saw %v; hidden tools must not reach it", etherpad.mcpCalls)
	}
}

func TestMCPRelayWaitsForEditsToSettle(t *testing.T) {
	gateway, etherpad, _, _ := newTestGateway(t)
	waitPollInterval = time.Millisecond
	t.Cleanup(func() { waitPollInterval = 5 * time.Second })
	pad := &fakePad{}
	pad.set("<!DOCTYPE HTML><html><body>isotty/notes<br>first<br></body></html>")
	etherpad.pads["pad1"] = pad

	go func() {
		time.Sleep(20 * time.Millisecond)
		etherpad.mu.Lock()
		pad.set("<!DOCTYPE HTML><html><body>isotty/notes<br>first<br>a human's remark<br></body></html>")
		etherpad.mu.Unlock()
	}()
	status, _, answer := mcpCall(t, gateway.URL, "wait_for_edits", map[string]any{"padId": "pad1", "settleSeconds": 0, "maxWaitSeconds": 5})
	text, isError := toolText(t, answer)
	if status != http.StatusOK || isError {
		t.Fatalf("status = %d, answer = %v", status, answer)
	}
	var result struct {
		FromRev    int      `json:"from_rev"`
		ToRev      int      `json:"to_rev"`
		AddedLines []string `json:"added_lines"`
		TimedOut   bool     `json:"timed_out"`
	}
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatal(err)
	}
	if result.TimedOut || result.FromRev != 1 || result.ToRev != 2 || strings.Join(result.AddedLines, "|") != "a human's remark" {
		t.Fatalf("result = %+v", result)
	}

	status, _, answer = mcpCall(t, gateway.URL, "wait_for_edits", map[string]any{"padId": "pad1", "sinceRev": 2, "maxWaitSeconds": 0})
	text, isError = toolText(t, answer)
	if err := json.Unmarshal([]byte(text), &result); status != http.StatusOK || isError || err != nil {
		t.Fatalf("status = %d, answer = %v", status, answer)
	}
	if !result.TimedOut || result.FromRev != 2 || result.ToRev != 2 || len(result.AddedLines) != 0 {
		t.Fatalf("result = %+v, want a timeout with nothing added", result)
	}

	_, _, answer = mcpCall(t, gateway.URL, "wait_for_edits", map[string]any{"padId": "pad1", "settleSeconds": -1})
	if text, isError := toolText(t, answer); !isError || text != "settleSeconds must be a non-negative integer" {
		t.Fatalf("answer = %v", answer)
	}
}

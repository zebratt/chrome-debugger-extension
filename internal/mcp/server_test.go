package mcp

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"chrome-connector/internal/protocol"
)

func TestMCPInitializesListsToolsAndCallsBroker(t *testing.T) {
	input := strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","clientInfo":{"name":"test","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"browser_list","arguments":{}}}`,
	}, "\n") + "\n")
	var output bytes.Buffer
	called := ""
	err := Run(input, &output, func(method string, params any) (protocol.Response, error) {
		called = method
		return protocol.Response{JSONRPC: "2.0", Result: map[string]any{"profiles": []any{}}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&output)
	var initialized, listed, result map[string]any
	for _, target := range []*map[string]any{&initialized, &listed, &result} {
		if err := decoder.Decode(target); err != nil {
			t.Fatal(err)
		}
	}
	if initialized["id"] != float64(1) || initialized["result"].(map[string]any)["protocolVersion"] != "2025-06-18" {
		t.Fatalf("initialize response %+v", initialized)
	}
	tools := listed["result"].(map[string]any)["tools"].([]any)
	if len(tools) < 5 {
		t.Fatalf("too few tools: %+v", tools)
	}
	if called != "browser.list" || result["id"] != float64(3) {
		t.Fatalf("tool call method %q result %+v", called, result)
	}
	content := result["result"].(map[string]any)["content"].([]any)
	if content[0].(map[string]any)["type"] != "text" {
		t.Fatalf("tool content %+v", content)
	}
}

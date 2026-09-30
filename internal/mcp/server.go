package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"chrome-connector/internal/automation"
	"chrome-connector/internal/client"
	"chrome-connector/internal/protocol"
	"chrome-connector/internal/version"
)

const Version = "2025-06-18"

type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Method      string         `json:"-"`
}

func tools() []tool {
	object := func(required []string) map[string]any {
		return map[string]any{"type": "object", "properties": map[string]any{
			"profileId": map[string]any{"type": "string"}, "tabId": map[string]any{"type": "integer"},
			"url": map[string]any{"type": "string"}, "leaseToken": map[string]any{"type": "string"},
			"snapshotId": map[string]any{"type": "string"}, "nodeRef": map[string]any{"type": "string"},
			"text": map[string]any{"type": "string"}, "key": map[string]any{"type": "string"},
			"deltaY": map[string]any{"type": "number"}, "timeoutMs": map[string]any{"type": "integer"},
			"method": map[string]any{"type": "string"}, "commandParams": map[string]any{"type": "object"},
			"guarded": map[string]any{"type": "boolean"}, "detailed": map[string]any{"type": "boolean"}, "optionIndex": map[string]any{"type": "integer", "minimum": 0},
			"replace": map[string]any{"type": "boolean"}, "compact": map[string]any{"type": "boolean"},
		}, "required": required, "additionalProperties": false}
	}
	return []tool{
		{"browser_list", "List connected Chrome profiles", object(nil), "browser.list"},
		{"browser_run", "Run exact-label search/navigation/fill or an explicit sequence of caller-authorized browser actions. Runs locally with no model calls. Reobserves targets, recovers pre-action stale state and verifies result conditions. Inspect completedActions, nextAction and steps before resuming; never replay unknown actions.", automation.InputSchema(), "browser.run"},
		{"tab_list", "List eligible tabs in a profile", object([]string{"profileId"}), "tab.list"},
		{"tab_open", "Open a web page and claim its tab; automatically renewed until released or this MCP session ends", object([]string{"profileId", "url"}), "tab.open"},
		{"tab_info", "Inspect an eligible tab", object([]string{"profileId", "tabId"}), "tab.info"},
		{"tab_snapshot", "Read the accessibility snapshot", object([]string{"profileId", "tabId"}), "tab.snapshot"},
		{"tab_screenshot", "Capture a PNG screenshot", object([]string{"profileId", "tabId"}), "tab.screenshot"},
		{"tab_claim", "Claim a tab before modifying it; automatically renewed until released or this MCP session ends", object([]string{"profileId", "tabId"}), "tab.claim"},
		{"tab_renew", "Renew a tab claim", object([]string{"profileId", "tabId", "leaseToken"}), "tab.renew"},
		{"tab_release", "Release a tab claim", object([]string{"profileId", "tabId", "leaseToken"}), "tab.release"},
		{"tab_navigate", "Navigate a claimed tab", object([]string{"profileId", "tabId", "url", "leaseToken"}), "tab.navigate"},
		{"tab_click", "Click a node from a snapshot", object([]string{"profileId", "tabId", "leaseToken", "snapshotId", "nodeRef"}), "tab.click"},
		{"tab_select", "Select an observed native dropdown option with target guards", object([]string{"profileId", "tabId", "leaseToken", "snapshotId", "nodeRef", "optionIndex"}), "tab.select"},
		{"tab_type", "Type into a node from a snapshot", object([]string{"profileId", "tabId", "leaseToken", "snapshotId", "nodeRef", "text"}), "tab.type"},
		{"tab_key", "Press a supported key", object([]string{"profileId", "tabId", "leaseToken", "key"}), "tab.key"},
		{"tab_scroll", "Scroll a claimed tab", object([]string{"profileId", "tabId", "leaseToken", "deltaY"}), "tab.scroll"},
		{"tab_wait", "Wait for text to appear", object([]string{"profileId", "tabId", "text"}), "tab.wait"},
		{"system_policy", "Read the active local policy", object(nil), "system.policy"},
		{"system_capabilities", "Read protocol and CDP capabilities", object(nil), "system.capabilities"},
		{"cdp_send", "Send a configured CDP command to a claimed tab", object([]string{"profileId", "tabId", "leaseToken", "method"}), "cdp.send"},
	}
}

func Run(input io.Reader, output io.Writer, call func(method string, params any) (protocol.Response, error)) error {
	return RunContext(context.Background(), input, output, func(_ context.Context, method string, params any) (protocol.Response, error) {
		return call(method, params)
	})
}

// RunContext releases this session's claims on EOF, protocol/output failure,
// or cancellation. It closes a closable input on exit to unblock its reader.
func RunContext(ctx context.Context, input io.Reader, output io.Writer, call func(context.Context, string, any) (protocol.Response, error)) (runErr error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	leases := client.NewLeaseSession(ctx, call)
	defer func() { runErr = errors.Join(runErr, leases.Close()) }()
	stop := context.AfterFunc(ctx, func() {
		if closer, ok := input.(io.Closer); ok {
			_ = closer.Close()
		}
	})
	defer func() {
		cancel()
		stop()
		if closer, ok := input.(io.Closer); ok {
			_ = closer.Close()
		}
	}()
	messages := readMessages(ctx, input)
	encoder := json.NewEncoder(output)
	for {
		var incoming incomingMessage
		select {
		case <-ctx.Done():
			return ctx.Err()
		case incoming = <-messages:
		}
		if incoming.err != nil {
			if incoming.err == io.EOF {
				return nil
			}
			return incoming.err
		}
		message := incoming.message
		if len(message.ID) == 0 {
			continue
		}
		var result any
		var rpcError any
		switch message.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": Version, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "chrome-connector", "version": version.Component}}
		case "ping":
			result = map[string]any{}
		case "tools/list":
			result = map[string]any{"tools": tools()}
		case "tools/call":
			var params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			if err := json.Unmarshal(message.Params, &params); err != nil {
				rpcError = map[string]any{"code": -32602, "message": err.Error()}
				break
			}
			method := ""
			for _, candidate := range tools() {
				if candidate.Name == params.Name {
					method = candidate.Method
					break
				}
			}
			if method == "" {
				rpcError = map[string]any{"code": -32602, "message": "unknown tool"}
				break
			}
			response, err := leases.Call(ctx, method, params.Arguments)
			if err != nil {
				result = map[string]any{"content": []any{map[string]any{"type": "text", "text": err.Error()}}, "isError": true}
			} else if response.Error != nil {
				encoded, _ := json.Marshal(response.Error)
				result = map[string]any{"content": []any{map[string]any{"type": "text", "text": string(encoded)}}, "isError": true}
			} else if method == "tab.screenshot" {
				image, ok := response.Result.(map[string]any)
				if !ok {
					result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "invalid screenshot response"}}, "isError": true}
				} else {
					result = map[string]any{"content": []any{map[string]any{"type": "image", "data": image["dataBase64"], "mimeType": image["mimeType"]}}}
				}
			} else {
				encoded, err := json.Marshal(response.Result)
				if err != nil {
					return err
				}
				result = map[string]any{"content": []any{map[string]any{"type": "text", "text": string(encoded)}}}
			}
		default:
			rpcError = map[string]any{"code": -32601, "message": fmt.Sprintf("unknown MCP method %s", message.Method)}
		}
		response := map[string]any{"jsonrpc": "2.0", "id": message.ID}
		if rpcError != nil {
			response["error"] = rpcError
		} else {
			response["result"] = result
		}
		if err := encoder.Encode(response); err != nil {
			return err
		}
	}
}

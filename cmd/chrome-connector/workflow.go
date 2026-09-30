package main

import (
	"context"
	"encoding/json"
	"time"

	"chrome-connector/internal/automation"
	"chrome-connector/internal/client"
	"chrome-connector/internal/protocol"
)

// The workflow lives in the requesting client process. The broker remains a
// transport; execution requires no model service or API credentials.
func callWithWorkflow(ctx context.Context, socket, method string, params any) (protocol.Response, error) {
	call := func(ctx context.Context, method string, params any) (protocol.Response, error) {
		timeout := 20 * time.Second
		if deadline, ok := ctx.Deadline(); ok {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				return protocol.Response{}, ctx.Err()
			}
			if remaining < timeout {
				timeout = remaining
			}
		}
		return client.CallContext(ctx, socket, method, params, timeout)
	}
	if method != "browser.run" {
		return call(ctx, method, params)
	}
	req, err := automation.ParseRequest(params)
	id := json.RawMessage(`"browser.run"`)
	if err != nil {
		return protocol.ErrorResponse(id, "INVALID_PARAMS", err.Error(), false), nil
	}
	runner := automation.Runner{Call: call}
	return protocol.Response{JSONRPC: "2.0", ID: id, Result: runner.Run(ctx, req)}, nil
}

func workflowNeedsAttention(response protocol.Response) bool {
	result, ok := response.Result.(automation.Result)
	return ok && result.Status != "completed"
}

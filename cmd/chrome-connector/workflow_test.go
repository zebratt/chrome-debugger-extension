package main

import (
	"chrome-connector/internal/automation"
	"chrome-connector/internal/protocol"
	"context"
	"testing"
)

func TestBrowserRunConvenienceUsesLocalWorkflow(t *testing.T) {
	args := expandConvenienceArgs([]string{"browser", "run", `{"workflow":"fill"}`})
	if len(args) != 3 || args[0] != "call" || args[1] != "browser.run" {
		t.Fatalf("args: %+v", args)
	}
	response, err := callWithWorkflow(context.Background(), "/nonexistent-test-socket", "browser.run", map[string]any{"workflow": "fill"})
	if err != nil || response.Error == nil || response.Error.Data.Kind != "INVALID_PARAMS" {
		t.Fatalf("request was not validated locally: %+v %v", response, err)
	}
}
func TestWorkflowAttentionIsNotSuccessfulCLICompletion(t *testing.T) {
	if !workflowNeedsAttention(protocol.Response{Result: automation.Result{Status: "needs_attention"}}) {
		t.Fatal("attention treated as success")
	}
	if workflowNeedsAttention(protocol.Response{Result: automation.Result{Status: "completed"}}) {
		t.Fatal("completion treated as attention")
	}
}

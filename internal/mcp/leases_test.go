package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"chrome-connector/internal/broker"
	"chrome-connector/internal/protocol"
)

func TestMCPSessionMaintainsAndReleasesClaimsAtEveryExit(t *testing.T) {
	for _, ending := range []string{"eof", "invalid-input", "cancel", "output-error"} {
		t.Run(ending, func(t *testing.T) {
			store := broker.NewLeaseStore(300*time.Millisecond, time.Now)
			var releases atomic.Int32
			call := func(ctx context.Context, method string, params any) (protocol.Response, error) {
				var p struct {
					Profile string `json:"profileId"`
					Tab     int    `json:"tabId"`
					Token   string `json:"leaseToken"`
				}
				raw, _ := json.Marshal(params)
				json.Unmarshal(raw, &p)
				var lease broker.Lease
				var err error
				switch method {
				case "tab.claim":
					lease, err = store.Claim(p.Profile, p.Tab, p.Token)
				case "tab.renew":
					lease, err = store.Renew(p.Profile, p.Tab, p.Token)
				case "tab.release":
					releases.Add(1)
					err = store.Release(p.Profile, p.Tab, p.Token)
				}
				if err != nil {
					return protocol.ErrorResponse(nil, "LEASE_EXPIRED", err.Error(), true), nil
				}
				return protocol.Response{Result: lease}, nil
			}
			input, send := io.Pipe()
			output, receive := io.Pipe()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			defer send.Close()
			defer output.Close()
			done := make(chan error, 1)
			go func() { done <- RunContext(ctx, input, receive, call); receive.Close() }()
			fmt.Fprintln(send, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"tab_claim","arguments":{"profileId":"p","tabId":1}}}`)
			var response struct {
				Result struct {
					Content []struct {
						Text string `json:"text"`
					} `json:"content"`
				} `json:"result"`
			}
			if err := json.NewDecoder(output).Decode(&response); err != nil {
				t.Fatal(err)
			}
			var lease broker.Lease
			if len(response.Result.Content) != 1 {
				t.Fatalf("response: %+v", response)
			}
			json.Unmarshal([]byte(response.Result.Content[0].Text), &lease)
			if ending == "eof" {
				time.Sleep(750 * time.Millisecond)
				if err := store.Check("p", 1, lease.Token); err != nil {
					t.Fatal(err)
				}
				if _, err := store.Claim("p", 1, ""); !errors.Is(err, broker.ErrTabBusy) {
					t.Fatal("claim was not retained")
				}
			}
			switch ending {
			case "eof":
				send.Close()
			case "invalid-input":
				fmt.Fprintln(send, "{invalid")
			case "cancel":
				cancel()
			case "output-error":
				output.Close()
				fmt.Fprintln(send, `{"jsonrpc":"2.0","id":2,"method":"ping"}`)
			}
			select {
			case err := <-done:
				if ending == "eof" && err != nil {
					t.Fatal(err)
				}
				if ending != "eof" && err == nil {
					t.Fatal("exit error hidden")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("MCP did not exit")
			}
			if releases.Load() != 1 || store.ActiveCount() != 0 {
				t.Fatalf("leases=%d releases=%d", store.ActiveCount(), releases.Load())
			}
			if _, err := store.Claim("p", 1, ""); err != nil {
				t.Fatal(err)
			}
		})
	}
}

package automation

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"chrome-connector/internal/broker"
	"chrome-connector/internal/protocol"
)

func TestWorkflowMaintainsLeaseDuringWaitAndCleansUp(t *testing.T) {
	for _, ending := range []string{"completed", "cancelled", "borrowed", "release-failure"} {
		t.Run(ending, func(t *testing.T) {
			store := broker.NewLeaseStore(300*time.Millisecond, time.Now)
			b := newScene(input("Name", ""))
			start := time.Now()
			b.afterRead = func(b *scene) {
				if time.Since(start) >= 750*time.Millisecond {
					b.text = "Ready"
				}
			}
			var renewals, releases atomic.Int32
			call := func(ctx context.Context, method string, params any) (protocol.Response, error) {
				p := params.(map[string]any)
				token, _ := p["leaseToken"].(string)
				var lease broker.Lease
				var err error
				switch method {
				case "tab.claim":
					lease, err = store.Claim("p", 1, token)
				case "tab.renew":
					renewals.Add(1)
					lease, err = store.Renew("p", 1, token)
				case "tab.release":
					releases.Add(1)
					if ending == "release-failure" {
						return protocol.Response{}, errors.New("connection lost")
					}
					err = store.Release("p", 1, token)
				default:
					if method == "tab.type" {
						if err := store.Check("p", 1, token); err != nil {
							return protocol.ErrorResponse(nil, "LEASE_EXPIRED", err.Error(), true), nil
						}
					}
					return b.call(ctx, method, params)
				}
				if err != nil {
					return protocol.ErrorResponse(nil, "LEASE_EXPIRED", err.Error(), true), nil
				}
				return protocol.Response{Result: lease}, nil
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			req := Request{ProfileID: "p", TabID: 1, Workflow: "sequence", TimeoutMS: 3000, Actions: []Action{
				{Type: "wait", Checks: Checks{ExpectText: "Ready"}},
				{Type: "type", Target: "Name", Text: str("Matt")},
			}}
			var borrowed broker.Lease
			if ending == "borrowed" {
				borrowed, _ = store.Claim("p", 1, "")
				req.LeaseToken = borrowed.Token
			}
			if ending == "cancelled" {
				timer := time.AfterFunc(350*time.Millisecond, cancel)
				defer timer.Stop()
			}
			got := (Runner{Call: call}).Run(ctx, req)
			switch ending {
			case "cancelled":
				if got.Status != "needs_attention" || got.Code != "TIMEOUT" || b.types != 0 {
					t.Fatalf("%+v", got)
				}
			case "release-failure":
				if got.Status != "needs_attention" || got.Code != "RELEASE_FAILED" || got.CompletedActions != 2 {
					t.Fatalf("%+v", got)
				}
			default:
				if got.Status != "completed" || b.types != 1 || renewals.Load() < 3 {
					t.Fatalf("result=%+v renewals=%d", got, renewals.Load())
				}
			}
			if ending == "borrowed" {
				if releases.Load() != 0 || store.Check("p", 1, borrowed.Token) != nil {
					t.Fatal("borrowed lease was released")
				}
			} else if ending != "release-failure" && (releases.Load() != 1 || store.ActiveCount() != 0) {
				t.Fatal("owned lease was leaked")
			}
		})
	}
}

package client

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"chrome-connector/internal/broker"
	"chrome-connector/internal/protocol"
)

type leaseBackend struct {
	store          *broker.LeaseStore
	mu             sync.Mutex
	calls          map[string]int
	renewFailure   string
	actionFailure  string
	releaseFailure bool
	readGate       <-chan struct{}
	renewGate      <-chan struct{}
}

func newLeaseBackend(ttl time.Duration) *leaseBackend {
	return &leaseBackend{store: broker.NewLeaseStore(ttl, time.Now), calls: make(map[string]int)}
}

func (b *leaseBackend) call(ctx context.Context, method string, params any) (protocol.Response, error) {
	var p leaseTarget
	decodeLeaseValue(params, &p)
	b.mu.Lock()
	b.calls[method]++
	renewFailure, actionFailure, releaseFailure := b.renewFailure, b.actionFailure, b.releaseFailure
	readGate, renewGate := b.readGate, b.renewGate
	b.mu.Unlock()
	var lease broker.Lease
	var err error
	switch method {
	case "tab.claim", "tab.open":
		if method == "tab.open" {
			p.TabID = 77
		}
		lease, err = b.store.Claim(p.ProfileID, p.TabID, p.Token)
	case "tab.renew":
		if renewGate != nil {
			select {
			case <-renewGate:
			case <-ctx.Done():
				return protocol.Response{}, ctx.Err()
			}
		}
		if renewFailure == "transport" {
			return protocol.Response{}, errors.New("connection lost")
		}
		if renewFailure != "" {
			return protocol.ErrorResponse(nil, renewFailure, "renewal failed", true), nil
		}
		lease, err = b.store.Renew(p.ProfileID, p.TabID, p.Token)
	case "tab.release":
		if releaseFailure {
			return protocol.Response{}, errors.New("connection lost")
		}
		err = b.store.Release(p.ProfileID, p.TabID, p.Token)
	case "tab.snapshot":
		if readGate != nil {
			select {
			case <-readGate:
			case <-ctx.Done():
				return protocol.Response{}, ctx.Err()
			}
		}
	default:
		if actionFailure != "" {
			return protocol.ErrorResponse(nil, actionFailure, "action interrupted", false), nil
		}
		err = b.store.Check(p.ProfileID, p.TabID, p.Token)
	}
	if err != nil {
		kind := "LEASE_EXPIRED"
		if errors.Is(err, broker.ErrTabBusy) {
			kind = "TAB_BUSY"
		}
		return protocol.ErrorResponse(nil, kind, err.Error(), true), nil
	}
	return protocol.Response{Result: map[string]any{"leaseToken": lease.Token, "expiresAt": lease.ExpiresAt, "tabId": p.TabID}}, nil
}

func (b *leaseBackend) count(method string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls[method]
}

func managedClaim(t *testing.T, s *LeaseSession, profile string, tab int, method string) leaseTarget {
	t.Helper()
	p := leaseTarget{ProfileID: profile, TabID: tab}
	r, err := s.Call(context.Background(), method, p.params())
	if err != nil || r.Error != nil {
		t.Fatalf("claim: %+v %v", r, err)
	}
	var result leaseTarget
	decodeLeaseValue(r.Result, &result)
	p.Token, p.TabID = result.Token, result.TabID
	return p
}

func eventually(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition did not become true")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestLeaseSessionKeepsIdleClaimAndReleasesOnClose(t *testing.T) {
	b := newLeaseBackend(300 * time.Millisecond)
	s := NewLeaseSession(context.Background(), b.call)
	t.Cleanup(func() { s.Close() })
	p := managedClaim(t, s, "p", 1, "tab.claim")
	time.Sleep(750 * time.Millisecond)
	if err := b.store.Check("p", 1, p.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := b.store.Claim("p", 1, ""); !errors.Is(err, broker.ErrTabBusy) {
		t.Fatalf("competing writer: %v", err)
	}
	if b.count("tab.renew") < 2 {
		t.Fatal("no periodic renewal")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if b.count("tab.release") != 1 {
		t.Fatal("close is not idempotent")
	}
	if _, err := b.store.Claim("p", 1, ""); err != nil {
		t.Fatal(err)
	}
	renews := b.count("tab.renew")
	time.Sleep(150 * time.Millisecond)
	if b.count("tab.renew") != renews {
		t.Fatal("heartbeat survived close")
	}
}

func TestLeaseSessionMaintainsClaimsDuringSlowRead(t *testing.T) {
	b := newLeaseBackend(300 * time.Millisecond)
	gate := make(chan struct{})
	b.readGate = gate
	s := NewLeaseSession(context.Background(), b.call)
	t.Cleanup(func() { s.Close() })
	p := managedClaim(t, s, "p", 1, "tab.claim")
	done := make(chan error, 1)
	go func() { _, err := s.Call(context.Background(), "tab.snapshot", p.params()); done <- err }()
	time.Sleep(750 * time.Millisecond)
	if err := b.store.Check("p", 1, p.Token); err != nil {
		t.Fatal(err)
	}
	close(gate)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestLeaseSessionBorrowedLeaseSurvivesClose(t *testing.T) {
	b := newLeaseBackend(300 * time.Millisecond)
	lease, _ := b.store.Claim("p", 1, "")
	s := NewLeaseSession(context.Background(), b.call)
	p := leaseTarget{"p", 1, lease.Token}
	r, err := s.Call(context.Background(), "tab.renew", p.params())
	if err != nil || r.Error != nil {
		t.Fatalf("renew: %+v %v", r, err)
	}
	eventually(t, func() bool { return b.count("tab.renew") >= 3 })
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if b.count("tab.release") != 0 {
		t.Fatal("released caller-owned lease")
	}
	if err := b.store.Check("p", 1, lease.Token); err != nil {
		t.Fatal(err)
	}
}

func TestLeaseSessionOpenMultipleProfilesAndExplicitRelease(t *testing.T) {
	b := newLeaseBackend(time.Second)
	s := NewLeaseSession(context.Background(), b.call)
	a := managedClaim(t, s, "a", 1, "tab.claim")
	managedClaim(t, s, "b", 1, "tab.claim")
	managedClaim(t, s, "a", 0, "tab.open")
	r, err := s.Call(context.Background(), "tab.release", a.params())
	if err != nil || r.Error != nil {
		t.Fatalf("release: %+v %v", r, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if b.store.ActiveCount() != 0 || b.count("tab.release") != 3 {
		t.Fatalf("remaining=%d release=%d", b.store.ActiveCount(), b.count("tab.release"))
	}
}

func TestLeaseSessionRenewalFailureBlocksWritesWithoutReclaim(t *testing.T) {
	for _, kind := range []string{"LEASE_EXPIRED", "BROWSER_OFFLINE", "transport"} {
		t.Run(kind, func(t *testing.T) {
			b := newLeaseBackend(300 * time.Millisecond)
			b.renewFailure = kind
			s := NewLeaseSession(context.Background(), b.call)
			t.Cleanup(func() { s.Close() })
			p := managedClaim(t, s, "p", 1, "tab.claim")
			eventually(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.leases[p].failure != nil })
			r, err := s.Call(context.Background(), "tab.click", p.params())
			want := kind
			if kind == "transport" {
				want = "LEASE_RENEW_FAILED"
			}
			if err != nil || r.Error == nil || r.Error.Data.Kind != want {
				t.Fatalf("write: %+v %v", r, err)
			}
			if b.count("tab.click") != 0 || b.count("tab.claim") != 1 {
				t.Fatal("reclaimed or replayed")
			}
			workflow, workflowErr := s.Call(context.Background(), "browser.run", p.params())
			if workflowErr != nil || workflow.Error == nil || workflow.Error.Data.Kind != want || b.count("browser.run") != 0 {
				t.Fatalf("workflow bypassed lease failure: %+v %v", workflow, workflowErr)
			}
			if _, err := s.Call(context.Background(), "tab.snapshot", p.params()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLeaseSessionUnknownActionStopsRenewingAndDoesNotReplay(t *testing.T) {
	b := newLeaseBackend(time.Second)
	b.actionFailure = "OUTCOME_UNKNOWN"
	s := NewLeaseSession(context.Background(), b.call)
	t.Cleanup(func() { s.Close() })
	p := managedClaim(t, s, "p", 1, "tab.claim")
	for i := 0; i < 2; i++ {
		r, err := s.Call(context.Background(), "tab.click", p.params())
		if err != nil || r.Error == nil || r.Error.Data.Kind != "OUTCOME_UNKNOWN" {
			t.Fatalf("write: %+v %v", r, err)
		}
	}
	if b.count("tab.click") != 1 {
		t.Fatal("unknown action replayed")
	}
}

func TestLeaseSessionCloseCancelsInflightRenewalBeforeRelease(t *testing.T) {
	b := newLeaseBackend(300 * time.Millisecond)
	b.renewGate = make(chan struct{})
	s := NewLeaseSession(context.Background(), b.call)
	managedClaim(t, s, "p", 1, "tab.claim")
	eventually(t, func() bool { return b.count("tab.renew") != 0 })
	start := time.Now()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second || b.store.ActiveCount() != 0 {
		t.Fatal("cleanup did not cancel renewal")
	}
}

func TestLeaseSessionReportsCleanupFailure(t *testing.T) {
	b := newLeaseBackend(time.Second)
	b.releaseFailure = true
	s := NewLeaseSession(context.Background(), b.call)
	managedClaim(t, s, "p", 1, "tab.claim")
	if err := s.Close(); err == nil {
		t.Fatal("cleanup failure was hidden")
	}
}

func TestLeaseSessionExplicitRenewRestartsAfterFailedRelease(t *testing.T) {
	b := newLeaseBackend(300 * time.Millisecond)
	b.releaseFailure = true
	s := NewLeaseSession(context.Background(), b.call)
	t.Cleanup(func() { s.Close() })
	p := managedClaim(t, s, "p", 1, "tab.claim")
	if _, err := s.Call(context.Background(), "tab.release", p.params()); err == nil {
		t.Fatal("release should fail")
	}
	b.mu.Lock()
	b.releaseFailure = false
	b.mu.Unlock()
	r, err := s.Call(context.Background(), "tab.renew", p.params())
	if err != nil || r.Error != nil {
		t.Fatalf("renew: %+v %v", r, err)
	}
	time.Sleep(750 * time.Millisecond)
	if err := b.store.Check("p", 1, p.Token); err != nil {
		t.Fatal(err)
	}
}

package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"chrome-connector/internal/protocol"
)

const leaseRequestTimeout = 2 * time.Second

type leaseTarget struct {
	ProfileID string `json:"profileId"`
	TabID     int    `json:"tabId"`
	Token     string `json:"leaseToken"`
}

func (t leaseTarget) params() map[string]any {
	return map[string]any{"profileId": t.ProfileID, "tabId": t.TabID, "leaseToken": t.Token}
}

type managedLease struct {
	target  leaseTarget
	owned   bool
	cancel  context.CancelFunc
	done    chan struct{}
	failure *protocol.RPCError
}

// LeaseSession maintains claims for one caller lifetime. The transport must
// support concurrent calls and honor context cancellation. Foreground calls are
// serialized, but heartbeats never wait for a page operation to finish.
type LeaseSession struct {
	ctx              context.Context
	cancel           context.CancelFunc
	call             func(context.Context, string, any) (protocol.Response, error)
	callMu           sync.Mutex
	mu               sync.Mutex
	leases           map[leaseTarget]*managedLease
	closeOnce        sync.Once
	closeErr         error
	maintenanceCalls atomic.Int64
}

func NewLeaseSession(ctx context.Context, call func(context.Context, string, any) (protocol.Response, error)) *LeaseSession {
	ctx, cancel := context.WithCancel(ctx)
	return &LeaseSession{ctx: ctx, cancel: cancel, call: call, leases: make(map[leaseTarget]*managedLease)}
}

func (s *LeaseSession) Call(ctx context.Context, method string, params any) (protocol.Response, error) {
	s.callMu.Lock()
	defer s.callMu.Unlock()
	if s.ctx.Err() != nil {
		return protocol.Response{}, s.ctx.Err()
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	defer cancel()
	var target leaseTarget
	decodeLeaseValue(params, &target)
	s.mu.Lock()
	lease := s.leases[target]
	var failure *protocol.RPCError
	if lease != nil {
		failure = lease.failure
	}
	s.mu.Unlock()
	if failure != nil && (method == "tab.renew" || method == "browser.run" || isAction(method)) {
		return protocol.Response{JSONRPC: "2.0", Error: failure}, nil
	}
	if method == "tab.release" && lease != nil {
		lease.cancel()
		<-lease.done
	}
	response, err := s.call(ctx, method, params)
	if err != nil {
		return response, err
	}
	if response.Error != nil {
		s.invalidate(target, response.Error)
		return response, nil
	}
	switch method {
	case "tab.claim", "tab.open", "tab.renew":
		var result struct {
			Token     string    `json:"leaseToken"`
			TabID     int       `json:"tabId"`
			ExpiresAt time.Time `json:"expiresAt"`
		}
		decodeLeaseValue(response.Result, &result)
		if result.Token != "" {
			target.Token = result.Token
		}
		if method == "tab.open" {
			target.TabID = result.TabID
		}
		s.track(target, result.ExpiresAt, method != "tab.renew")
	case "tab.release":
		s.mu.Lock()
		delete(s.leases, target)
		s.mu.Unlock()
	}
	return response, nil
}

func decodeLeaseValue(value, target any) {
	data, err := json.Marshal(value)
	if err == nil {
		_ = json.Unmarshal(data, target)
	}
}

func (s *LeaseSession) track(target leaseTarget, expires time.Time, owned bool) {
	if target.ProfileID == "" || target.TabID <= 0 || target.Token == "" {
		return
	}
	s.mu.Lock()
	if old := s.leases[target]; old != nil {
		old.owned = old.owned || owned
		// An explicit successful claim may re-establish this token. A failed
		// heartbeat is never silently recovered by a write operation.
		if old.failure == nil {
			select {
			case <-old.done:
				// An explicit successful renew after a failed release restarts maintenance.
			default:
				s.mu.Unlock()
				return
			}
		}
		owned = old.owned
		old.cancel()
	}
	ctx, cancel := context.WithCancel(s.ctx)
	lease := &managedLease{target: target, owned: owned, cancel: cancel, done: make(chan struct{})}
	s.leases[target] = lease
	s.mu.Unlock()
	interval := 10 * time.Second
	if !expires.IsZero() {
		if remaining := time.Until(expires) / 3; remaining < interval {
			interval = remaining
		}
	}
	if interval <= 0 {
		interval = time.Millisecond
	}
	go s.heartbeat(ctx, lease, interval)
}

func (s *LeaseSession) heartbeat(ctx context.Context, lease *managedLease, interval time.Duration) {
	defer close(lease.done)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if ctx.Err() != nil {
				return
			}
			requestCtx, cancel := context.WithTimeout(ctx, leaseRequestTimeout)
			s.maintenanceCalls.Add(1)
			response, err := s.call(requestCtx, "tab.renew", lease.target.params())
			cancel()
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				response = protocol.ErrorResponse(nil, "LEASE_RENEW_FAILED", "automatic lease renewal failed; inspect the connection and reclaim the tab before writing", true)
			}
			if response.Error != nil {
				s.mu.Lock()
				lease.failure = response.Error
				s.mu.Unlock()
				return
			}
		}
	}
}

func (s *LeaseSession) invalidate(target leaseTarget, failure *protocol.RPCError) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, lease := range s.leases {
		matches := key == target
		switch failure.Data.Kind {
		case "BROWSER_OFFLINE":
			matches = key.ProfileID == target.ProfileID
		case "TARGET_DETACHED", "OUTCOME_UNKNOWN":
			matches = key.ProfileID == target.ProfileID && key.TabID == target.TabID
		case "LEASE_EXPIRED":
		default:
			continue
		}
		if matches {
			lease.failure = failure
			lease.cancel()
		}
	}
}

// Close first stops all renewals, then releases only claims created by this
// session. All releases share one deadline, even with multiple Chrome profiles.
func (s *LeaseSession) Close() error {
	const releaseMessage = "could not release tab %d in profile %s"
	s.closeOnce.Do(func() {
		s.cancel()
		s.callMu.Lock()
		defer s.callMu.Unlock()
		s.mu.Lock()
		leases := make([]*managedLease, 0, len(s.leases))
		for _, lease := range s.leases {
			lease.cancel()
			leases = append(leases, lease)
		}
		s.mu.Unlock()
		for _, lease := range leases {
			<-lease.done
		}
		ctx, cancel := context.WithTimeout(context.Background(), leaseRequestTimeout)
		defer cancel()
		results := make(chan error, len(leases))
		for _, lease := range leases {
			if !lease.owned {
				results <- nil
				continue
			}
			go func(lease *managedLease) {
				s.maintenanceCalls.Add(1)
				response, err := s.call(ctx, "tab.release", lease.target.params())
				if err != nil || response.Error != nil {
					// A different token now owns this tab; our old token cannot
					// release it and no longer needs cleanup.
					if err == nil && response.Error.Data.Kind == "TAB_BUSY" {
						results <- nil
						return
					}
					results <- fmt.Errorf(releaseMessage, lease.target.TabID, lease.target.ProfileID)
					return
				}
				results <- nil
			}(lease)
		}
		for range leases {
			s.closeErr = errors.Join(s.closeErr, <-results)
		}
	})
	return s.closeErr
}

// MaintenanceCalls includes background renewal and cleanup RPCs, so a workflow
// can report its total browser calls without sharing mutable metrics.
func (s *LeaseSession) MaintenanceCalls() int {
	return int(s.maintenanceCalls.Load())
}

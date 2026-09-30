package automation

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"
)

type session struct {
	runner        Runner
	req           Request
	result        Result
	origin, lease string
	last          Snapshot
	claims        []tabClaim
	priorTabs     map[int]bool
	tabTrackingAt time.Time
	expectedChild bool
}

func (r Runner) Run(ctx context.Context, req Request) (result Result) {
	start := time.Now()
	s := &session{runner: r, req: req, result: Result{Status: "needs_attention", TabID: req.TabID, Steps: []Step{}}}
	if req.TimeoutMS == 0 {
		s.req.TimeoutMS = 15000
		if req.Workflow == "agent" {
			s.req.TimeoutMS = 60000
		}
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(s.req.TimeoutMS)*time.Millisecond)
	defer cancel()
	err := req.validate()
	if err == nil && r.Call == nil {
		err = errors.New("browser connection is unavailable")
	}
	if err == nil {
		err = s.execute(ctx)
	}
	if err != nil {
		var f *failure
		if errors.As(err, &f) {
			s.result.Code = f.code
			s.result.Message = f.message
		} else {
			s.result.Code = "INVALID_REQUEST"
			s.result.Message = err.Error()
		}
	}
	for _, claim := range s.claims {
		cleanup, stop := context.WithTimeout(context.Background(), 2*time.Second)
		_, releaseErr := s.rpc(cleanup, "tab.release", map[string]any{"tabId": claim.id, "leaseToken": claim.token})
		stop()
		if releaseErr != nil && s.result.Status == "completed" {
			s.result.Status = "needs_attention"
			s.result.Code = "RELEASE_FAILED"
			s.result.Message = "steps finished but a tab claim could not be released"
		}
	}

	s.result.Metrics.DurationMS = time.Since(start).Milliseconds()
	return s.result
}
func (s *session) rpc(ctx context.Context, method string, extra map[string]any) (json.RawMessage, error) {
	if ctx.Err() != nil {
		return nil, fail("TIMEOUT", "browser workflow was cancelled or timed out; inspect the page before continuing")
	}
	p := map[string]any{"profileId": s.req.ProfileID, "tabId": s.req.TabID}
	for k, v := range extra {
		p[k] = v
	}
	s.result.Metrics.BrowserCalls++
	response, err := s.runner.Call(ctx, method, p)
	if err != nil {
		return nil, fail("BROWSER_UNAVAILABLE", "browser request failed; inspect the page before continuing")
	}
	if response.Error != nil {
		return nil, fail(response.Error.Data.Kind, response.Error.Message)
	}
	raw, err := json.Marshal(response.Result)
	if err != nil {
		return nil, fail("INVALID_BROWSER_RESPONSE", "invalid browser response")
	}
	return raw, nil
}
func origin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return ""
	}
	return strings.ToLower(u.Scheme + "://" + u.Host)
}
func (s *session) snapshotOnce(ctx context.Context) (Snapshot, error) {
	var snap Snapshot
	raw, err := s.rpc(ctx, "tab.info", nil)
	if err != nil {
		return snap, err
	}
	var info struct {
		URL string `json:"url"`
	}
	if json.Unmarshal(raw, &info) != nil || origin(info.URL) == "" {
		return snap, fail("INVALID_BROWSER_RESPONSE", "tab URL is unavailable")
	}
	s.result.URL = info.URL
	if s.origin == "" && s.req.Workflow != "sequence" {
		s.origin = origin(info.URL)
	}
	if s.origin != "" && !s.originAllowed(info.URL) {
		return snap, fail("ORIGIN_CHANGED", "tab moved to a different origin; return to Codex before continuing")
	}
	raw, err = s.rpc(ctx, "tab.snapshot", map[string]any{"compact": true, "detailed": true})
	if err != nil {
		return snap, err
	}
	if json.Unmarshal(raw, &snap) != nil || snap.ID == "" {
		return snap, fail("INVALID_BROWSER_RESPONSE", "snapshot is invalid")
	}
	if snap.Version < 1 {
		return snap, fail("EXTENSION_UPGRADE_REQUIRED", "update and reload the Chrome extension for workflow metadata")
	}
	if s.origin != "" && !s.originAllowed(snap.URL) {
		return snap, fail("ORIGIN_CHANGED", "tab changed origin during observation")
	}
	if snap.URL != info.URL {
		return snap, fail("STALE_SNAPSHOT", "tab URL changed during observation")
	}
	// An initial read may race an already-requested navigation. Bind the start
	// origin only after both tab metadata and the document snapshot agree.
	if s.origin == "" {
		s.origin = origin(snap.URL)
	}
	s.result.URL = snap.URL
	s.result.TabID = s.req.TabID
	s.last = snap
	return snap, nil
}
func (s *session) acquire(ctx context.Context) error {
	if s.req.LeaseToken != "" {
		s.lease = s.req.LeaseToken
		_, err := s.rpc(ctx, "tab.renew", map[string]any{"leaseToken": s.lease})
		return err
	}
	raw, err := s.rpc(ctx, "tab.claim", nil)
	if err != nil {
		return err
	}
	var v struct {
		Token string `json:"leaseToken"`
	}
	if json.Unmarshal(raw, &v) != nil || v.Token == "" {
		return fail("INVALID_BROWSER_RESPONSE", "claim did not return a lease token")
	}
	s.lease = v.Token
	s.claims = append(s.claims, tabClaim{s.req.TabID, s.lease})
	return nil
}
func (s *session) act(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	if _, err := s.rpc(ctx, "tab.renew", map[string]any{"leaseToken": s.lease}); err != nil {
		return nil, err
	}
	params["leaseToken"] = s.lease
	return s.rpc(ctx, method, params)
}
func (s *session) execute(ctx context.Context) error {
	snap, err := s.snapshot(ctx)
	if err != nil {
		return err
	}
	if snap.Version < 2 {
		return fail("EXTENSION_UPGRADE_REQUIRED", "continuous workflows require extension 0.4.0 detailed snapshots")
	}
	if err = s.acquire(ctx); err != nil {
		return err
	}
	baseline := snap
	actions := s.req.Actions
	outcome := "executed"
	switch s.req.Workflow {
	case "fill":
		outcome = "filled"
		for _, f := range s.req.Fields {
			text := f.Text
			actions = append(actions, Action{Type: "type", Target: f.Target, Text: &text})
		}
	case "search":
		text := s.req.Text
		actions = []Action{{Type: "type", Target: s.req.Target, Text: &text}}
		if s.req.PressEnter {
			actions = append(actions, Action{Type: "key", Target: s.req.Target, Key: "Enter"})
		}
	case "navigate":
		a := Action{Type: "click", Target: s.req.Target, Role: "link"}
		n, e := findTarget(snap, a)
		if e != nil {
			return e
		}
		if !s.originAllowed(n.Href) {
			return fail("ORIGIN_CHANGE_REQUIRED", "link destination requires explicit allowedOrigins")
		}
		actions = []Action{a}
		outcome = "navigated"
		if s.req.ExpectURL == "" {
			s.req.ExpectURL = n.Href
		}
	}
	snap, err = s.runActions(ctx, snap, actions)
	if err != nil {
		return err
	}
	if s.req.Workflow == "search" {
		err = s.waitSearch(ctx, baseline, snap)
		if err != nil {
			return err
		}
		outcome = "matched"
		if s.req.EmptyText != "" && strings.Contains(strings.Join(visibleText(s.last), "\n"), s.req.EmptyText) {
			outcome = "empty"
		}
	} else if s.req.Checks.configured() {
		if _, err = s.waitChecks(ctx, snap, s.req.Checks, "", 10*time.Second); err != nil {
			return err
		}
		outcome = "verified"
	}
	s.result.Status = "completed"
	s.result.Outcome = outcome
	return nil
}

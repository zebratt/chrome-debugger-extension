package automation

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

type tabClaim struct {
	id    int
	token string
}
type tabInfo struct {
	ID     int    `json:"tabId"`
	Opener int    `json:"openerTabId"`
	URL    string `json:"url"`
}

func (s *session) originAllowed(raw string) bool {
	o := origin(raw)
	if o == "" {
		return false
	}
	if o == s.origin {
		return true
	}
	if s.req.Workflow == "sequence" {
		for _, allowed := range s.req.AllowedOrigins {
			if o == origin(allowed) {
				return true
			}
		}
	}
	return false
}
func errorKind(err error) string {
	var f *failure
	if errors.As(err, &f) {
		return f.code
	}
	return ""
}
func delay(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return fail("TIMEOUT", "workflow deadline reached")
	case <-timer.C:
		return nil
	}
}
func (s *session) snapshot(ctx context.Context) (Snapshot, error) {
	for attempt := 0; attempt < 5; attempt++ {
		snap, err := s.snapshotOnce(ctx)
		if err == nil {
			return snap, nil
		}
		switch errorKind(err) {
		case "STALE_SNAPSHOT", "TARGET_DETACHED", "CHROME_ERROR", "OUTCOME_UNKNOWN":
		default:
			return snap, err
		}
		if attempt == 4 {
			return snap, err
		}
		s.result.Metrics.ReadRecoveries++
		if err := delay(ctx, time.Duration(attempt+1)*100*time.Millisecond); err != nil {
			return snap, err
		}
	}
	return Snapshot{}, fail("UNSTABLE_PAGE", "page did not settle")
}
func (s *session) tabs(ctx context.Context) ([]tabInfo, error) {
	raw, err := s.rpc(ctx, "tab.list", nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Tabs []tabInfo `json:"tabs"`
	}
	if json.Unmarshal(raw, &result) != nil {
		return nil, fail("INVALID_BROWSER_RESPONSE", "invalid tab inventory")
	}
	return result.Tabs, nil
}
func (s *session) trackTabs(ctx context.Context) error {
	tabs, err := s.tabs(ctx)
	if err != nil {
		return err
	}
	s.priorTabs = map[int]bool{}
	s.tabTrackingAt = time.Now()
	for _, tab := range tabs {
		s.priorTabs[tab.ID] = true
	}
	return nil
}
func (s *session) followChild(ctx context.Context) (bool, error) {
	if s.priorTabs == nil || time.Since(s.tabTrackingAt) > 5*time.Second {
		return false, nil
	}
	tabs, err := s.tabs(ctx)
	if err != nil {
		return false, err
	}
	var children []tabInfo
	for _, tab := range tabs {
		if !s.priorTabs[tab.ID] && tab.Opener == s.req.TabID {
			children = append(children, tab)
		}
	}
	if len(children) == 0 {
		var fresh []tabInfo
		for _, tab := range tabs {
			if !s.priorTabs[tab.ID] {
				fresh = append(fresh, tab)
			}
		}
		if len(fresh) > 0 {
			if len(fresh) == 1 {
				s.result.NextTabID = fresh[0].ID
			}
			return false, fail("UNATTRIBUTED_NEW_TAB", "a new tab appeared without a matching opener; inspect it instead of guessing ownership")
		}
		return false, nil
	}
	if len(children) != 1 {
		return false, fail("AMBIGUOUS_NEW_TAB", "multiple child tabs opened; return to Codex")
	}
	child := children[0]
	s.result.NextTabID = child.ID
	if s.req.Workflow != "sequence" || !s.req.FollowNewTabs {
		return false, fail("NEW_TAB_OPENED", "the action opened a child tab; continue on nextTabId after inspection")
	}
	if !s.originAllowed(child.URL) {
		return false, fail("ORIGIN_CHANGE_REQUIRED", "child tab origin is outside allowedOrigins")
	}
	s.req.TabID = child.ID
	s.req.LeaseToken = ""
	s.lease = ""
	s.priorTabs = nil
	if err := s.acquire(ctx); err != nil {
		return false, err
	}
	s.result.TabID = child.ID
	s.result.URL = child.URL
	s.result.NextTabID = 0
	return true, nil
}

func (s *session) observeChild(ctx context.Context) (bool, error) {
	if !s.expectedChild {
		return s.followChild(ctx)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		followed, err := s.followChild(ctx)
		if err != nil || followed {
			s.expectedChild = false
			return followed, err
		}
		if err := delay(ctx, 100*time.Millisecond); err != nil {
			return false, err
		}
	}
	s.expectedChild = false
	return false, fail("NEW_TAB_NOT_FOUND", "the new-tab link was clicked but its child did not become observable; inspect before continuing")
}

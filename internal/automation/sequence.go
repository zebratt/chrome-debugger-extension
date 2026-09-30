package automation

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

func (s *session) waitChecks(ctx context.Context, snap Snapshot, c Checks, empty string, window time.Duration) (Snapshot, error) {
	deadline := time.Now().Add(window)
	for {
		if c.configured() && c.matches(snap) || empty != "" && strings.Contains(strings.Join(visibleText(snap), "\n"), empty) {
			return snap, nil
		}
		if !time.Now().Before(deadline) {
			return snap, fail("EXPECTATION_UNMET", "explicit result conditions were not met; executed actions will not be replayed")
		}
		if err := delay(ctx, 100*time.Millisecond); err != nil {
			return snap, err
		}
		if _, err := s.followChild(ctx); err != nil {
			return snap, err
		}
		fresh, err := s.snapshot(ctx)
		if err != nil {
			return snap, err
		}
		snap = fresh
	}
}
func (s *session) locate(ctx context.Context, snap Snapshot, a Action) (Snapshot, Node, error) {
	window := 3 * time.Second
	if a.TimeoutMS > 0 {
		window = time.Duration(a.TimeoutMS) * time.Millisecond
	}
	deadline := time.Now().Add(window)
	for {
		var readyErr error
		snap, readyErr = s.waitDocument(ctx, snap)
		if readyErr != nil {
			return snap, Node{}, readyErr
		}
		n, err := findTarget(snap, a)
		if err == nil || errorKind(err) != "NO_MATCH" || !time.Now().Before(deadline) {
			return snap, n, err
		}
		if err = delay(ctx, 100*time.Millisecond); err != nil {
			return snap, Node{}, err
		}
		if _, err = s.followChild(ctx); err != nil {
			return snap, Node{}, err
		}
		snap, err = s.snapshot(ctx)
		if err != nil {
			return snap, Node{}, err
		}
	}
}
func (s *session) runActions(ctx context.Context, snap Snapshot, actions []Action) (Snapshot, error) {
	for index, a := range actions {
		s.result.NextAction = index
		retries := 0
		for {
			var err error
			if a.Type == "scroll" || a.Type == "wait" || a.Type == "assert" {
				snap, err = s.waitDocument(ctx, snap)
				if err != nil {
					return snap, err
				}
			}
			step := Step{Index: index, Action: a.Type, TabID: s.req.TabID, Execution: "observed"}
			if a.Type == "wait" || a.Type == "assert" {
				if a.Type == "wait" {
					window := 3 * time.Second
					if a.TimeoutMS > 0 {
						window = time.Duration(a.TimeoutMS) * time.Millisecond
					}
					snap, err = s.waitChecks(ctx, snap, a.Checks, "", window)
				} else if !a.Checks.matches(snap) {
					err = fail("EXPECTATION_UNMET", "assertion failed")
				}
				step.Verified = err == nil
				s.result.Steps = append(s.result.Steps, step)
				if err != nil {
					return snap, err
				}
				s.result.CompletedActions++
				s.result.NextAction = index + 1
				break
			}
			method := "tab." + a.Type
			params := map[string]any{"snapshotId": snap.ID}
			skip := false
			if a.Type == "scroll" {
				params["deltaY"] = *a.DeltaY
			} else {
				var n Node
				snap, n, err = s.locate(ctx, snap, a)
				if err != nil {
					return snap, err
				}
				if n.Role == "link" && !s.originAllowed(n.Href) {
					return snap, fail("ORIGIN_CHANGE_REQUIRED", "link destination is outside allowedOrigins")
				}
				step.Target = a.Target
				step.MatchedName = n.Name
				step.Selection = "exact"
				params["snapshotId"] = snap.ID
				params["nodeRef"] = n.Ref
				params["guarded"] = true
				switch a.Type {
				case "type":
					params["text"] = *a.Text
					params["replace"] = true
					skip = n.Value != nil && *n.Value == *a.Text
				case "key":
					params["key"] = a.Key
				case "select":
					if n.OptionsTruncated {
						return snap, fail("TOO_MANY_OPTIONS", "dropdown observation is truncated")
					}
					var options []SelectOption
					for _, o := range n.Options {
						if !o.Disabled && (o.Value == *a.Option || normalize(o.Label) == normalize(*a.Option)) {
							options = append(options, o)
						}
					}
					if len(options) != 1 {
						return snap, fail("AMBIGUOUS_OPTION", "select requires exactly one matching observed option")
					}
					params["optionIndex"] = options[0].Index
					skip = options[0].Selected
				}
				if !skip && (a.Type == "click" || a.Type == "key") {
					s.expectedChild = n.OpensNewTab
					if err = s.trackTabs(ctx); err != nil {
						return snap, err
					}
				}
			}
			if skip {
				step.Verified = true
				s.result.Steps = append(s.result.Steps, step)
			} else {
				step.Execution = "attempted"
				s.result.Steps = append(s.result.Steps, step)
				i := len(s.result.Steps) - 1
				raw, actErr := s.act(ctx, method, params)
				if actErr != nil {
					s.result.Steps[i].Note = actErr.Error()
					if errorKind(actErr) == "STALE_SNAPSHOT" {
						s.result.Steps[i].Execution = "not_executed"
						retries++
						s.result.Metrics.ActionRecoveries++
						if retries > 3 {
							return snap, fail("UNSTABLE_TARGET", "target repeatedly changed before execution")
						}
						snap, err = s.snapshot(ctx)
						if err != nil {
							return snap, err
						}
						continue
					}
					s.result.Steps[i].Execution = "unknown"
					switch errorKind(actErr) {
					case "LEASE_EXPIRED", "TAB_BUSY", "POLICY_DENIED", "INVALID_PARAMS":
						s.result.Steps[i].Execution = "not_executed"
					}
					return snap, actErr
				}
				if a.Type == "type" || a.Type == "select" {
					var response struct {
						Verified bool `json:"verified"`
					}
					if json.Unmarshal(raw, &response) != nil || !response.Verified {
						s.result.Steps[i].Execution = "executed_unverified"
						return snap, fail("INPUT_UNVERIFIED", "input was attempted but not verified; inspect before continuing")
					}
				}
				s.result.Steps[i].Execution = "executed"
				s.result.Steps[i].Verified = true
			}
			// Commit the completed prefix before any potentially failing observation.
			s.result.CompletedActions++
			s.result.NextAction = index + 1
			if _, err = s.observeChild(ctx); err != nil {
				return snap, err
			}
			snap, err = s.snapshot(ctx)
			if err != nil {
				return snap, err
			}
			break
		}
	}
	return snap, nil
}

// Search must produce new result evidence; a pre-existing result message alone
// cannot verify that the current query has been applied.
func (s *session) waitSearch(ctx context.Context, before, snap Snapshot) error {
	old := strings.Join(visibleText(before), "\n")
	for {
		text := strings.Join(visibleText(snap), "\n")
		if s.req.EmptyText != "" && strings.Contains(text, s.req.EmptyText) && !strings.Contains(old, s.req.EmptyText) {
			return nil
		}
		fresh := snap.URL != before.URL || s.req.ExpectText != "" && !strings.Contains(old, s.req.ExpectText)
		if fresh && s.req.Checks.configured() && s.req.Checks.matches(snap) {
			return nil
		}
		if err := delay(ctx, 100*time.Millisecond); err != nil {
			return err
		}
		if _, err := s.followChild(ctx); err != nil {
			return err
		}
		var err error
		snap, err = s.snapshot(ctx)
		if err != nil {
			return err
		}
	}
}

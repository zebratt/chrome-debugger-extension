package automation

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

func (c Checks) matches(snap Snapshot) bool {
	if snap.Version >= 2 && snap.ReadyState != "complete" && snap.ReadyState != "interactive" {
		return false
	}
	if c.ExpectURL != "" && !equalURL(snap.URL, c.ExpectURL) {
		return false
	}
	if c.ExpectText != "" && !strings.Contains(strings.Join(visibleText(snap), "\n"), c.ExpectText) {
		return false
	}
	for _, field := range c.VerifyFields {
		var matches []Node
		for _, n := range snap.Nodes {
			if !n.Ignored && normalize(n.Name) == normalize(field.Target) && (n.Editable || n.InputType == "select" || n.Role == "checkbox" || n.Role == "radio" || n.Role == "switch") {
				matches = append(matches, n)
			}
		}
		if len(matches) != 1 {
			return false
		}
		n := matches[0]
		ok := currentValue(n) == field.Text
		if n.InputType == "select" {
			for _, o := range n.Options {
				if o.Selected && o.Label == field.Text {
					ok = true
				}
			}
		}
		if !ok {
			return false
		}
	}
	return true
}
func currentValue(n Node) string {
	if n.Value != nil {
		return *n.Value
	}
	if n.InputType == "select" {
		for _, o := range n.Options {
			if o.Selected {
				return o.Value
			}
		}
	}
	if n.Checked != nil {
		return fmt.Sprint(n.Checked)
	}
	return ""
}
func snapshotFingerprint(snap Snapshot) string {
	nodes := make([]any, 0, len(snap.Nodes))
	for _, n := range snap.Nodes {
		if n.Ignored {
			continue
		}
		nodes = append(nodes, []any{n.Role, n.Name, n.Context, n.Value, n.Checked, n.Selected, n.Expanded, n.Href, n.Visible, n.Options})
	}
	data, _ := json.Marshal([]any{snap.DocumentKey, snap.URL, snap.ReadyState, snap.Text, snap.ScrollY, nodes})
	return fmt.Sprintf("%x", sha256.Sum256(data))
}
func equalURL(a, b string) bool {
	left, e1 := url.Parse(a)
	right, e2 := url.Parse(b)
	if e1 != nil || e2 != nil {
		return false
	}
	lp, rp := left.EscapedPath(), right.EscapedPath()
	if lp == "" {
		lp = "/"
	}
	if rp == "" {
		rp = "/"
	}
	return strings.EqualFold(left.Scheme, right.Scheme) && strings.EqualFold(left.Host, right.Host) && lp == rp && left.RawQuery == right.RawQuery && left.ForceQuery == right.ForceQuery && left.Fragment == right.Fragment
}

func (s *session) waitDocument(ctx context.Context, snap Snapshot) (Snapshot, error) {
	deadline := time.Now().Add(10 * time.Second)
	stableSince := time.Now()
	previous := snapshotFingerprint(snap)
	for snap.ReadyState != "complete" {
		// Parsed pages can remain interactive while unrelated resources load.
		// Require a stable observed document before acting on that state.
		if snap.ReadyState == "interactive" && time.Since(stableSince) >= 500*time.Millisecond {
			return snap, nil
		}
		if !time.Now().Before(deadline) {
			return snap, fail("PAGE_NOT_READY", "document did not become ready and stable; no further input was attempted")
		}
		if err := delay(ctx, 150*time.Millisecond); err != nil {
			return snap, err
		}
		fresh, err := s.snapshot(ctx)
		if err != nil {
			return snap, err
		}
		fingerprint := snapshotFingerprint(fresh)
		if fingerprint != previous || fresh.ReadyState != "interactive" {
			stableSince = time.Now()
		}
		previous = fingerprint
		snap = fresh
	}
	return snap, nil
}

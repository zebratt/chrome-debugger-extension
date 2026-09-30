package automation

import "strings"

// The caller supplies the intended target. Matching never guesses between
// multiple controls or reads a remote model to resolve ambiguity.
func findTarget(snap Snapshot, a Action) (Node, error) {
	var matches []Node
	protected := false
	for _, n := range snap.Nodes {
		if n.Ref == "" || n.Ignored {
			continue
		}
		if a.Target != "" && normalize(n.Name) != normalize(a.Target) {
			continue
		}
		if a.Role != "" && n.Role != a.Role {
			continue
		}
		if a.Href != "" && !equalURL(n.Href, a.Href) {
			continue
		}
		eligible := false
		switch a.Type {
		case "type", "key":
			eligible = n.Editable && (n.Role == "textbox" || n.Role == "searchbox" || n.Role == "combobox")
			switch n.InputType {
			case "text", "search", "email", "tel", "url", "textarea":
			default:
				eligible = false
			}
			if a.Target != "" && !eligible && (n.Role == "textbox" || n.Role == "searchbox" || n.Role == "combobox") {
				protected = true
			}
		case "select":
			eligible = n.InputType == "select"
		case "click":
			switch n.Role {
			case "button", "link", "textbox", "searchbox", "combobox", "checkbox", "radio", "switch", "tab", "menuitem", "option", "gridcell":
				eligible = true
			}
			// Nonstandard controls are usable only when the caller explicitly names
			// their observed role. Runtime guards still check meaning and hit geometry.
			if a.Role != "" {
				eligible = true
			}
		}
		if eligible && !n.Disabled {
			matches = append(matches, n)
		}
	}
	if protected {
		return Node{}, fail("PROTECTED_TARGET", "the named input is not an enabled supported text field")
	}
	if len(matches) == 0 {
		return Node{}, fail("NO_MATCH", "no eligible exact target was found")
	}
	if len(matches) != 1 {
		return Node{}, fail("AMBIGUOUS_TARGET", "target matches multiple nodes; provide an exact role or href")
	}
	return matches[0], nil
}
func visibleText(s Snapshot) []string {
	if s.Version >= 2 {
		if s.Text == "" {
			return nil
		}
		return strings.Split(s.Text, "\n")
	}
	seen := map[string]bool{}
	var lines []string
	for _, n := range s.Nodes {
		if n.Ignored {
			continue
		}
		switch n.Role {
		case "StaticText", "heading", "status", "alert":
			text := strings.TrimSpace(n.Name)
			if text != "" && !seen[text] {
				lines = append(lines, text)
				seen[text] = true
			}
		}
	}
	return lines
}

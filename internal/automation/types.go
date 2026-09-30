package automation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"

	"chrome-connector/internal/protocol"
)

type Field struct {
	Target string `json:"target"`
	Text   string `json:"text"`
}
type Checks struct {
	ExpectText   string  `json:"expectText,omitempty"`
	ExpectURL    string  `json:"expectURL,omitempty"`
	VerifyFields []Field `json:"verifyFields,omitempty"`
}
type Action struct {
	Type      string   `json:"type"`
	Target    string   `json:"target,omitempty"`
	Role      string   `json:"role,omitempty"`
	Href      string   `json:"href,omitempty"`
	Text      *string  `json:"text,omitempty"`
	Option    *string  `json:"option,omitempty"`
	Key       string   `json:"key,omitempty"`
	DeltaY    *float64 `json:"deltaY,omitempty"`
	TimeoutMS int      `json:"timeoutMs,omitempty"`
	Checks
}
type Request struct {
	ProfileID      string   `json:"profileId"`
	TabID          int      `json:"tabId"`
	Workflow       string   `json:"workflow"`
	Target         string   `json:"target,omitempty"`
	Text           string   `json:"text,omitempty"`
	Fields         []Field  `json:"fields,omitempty"`
	EmptyText      string   `json:"emptyText,omitempty"`
	PressEnter     bool     `json:"pressEnter,omitempty"`
	LeaseToken     string   `json:"leaseToken,omitempty"`
	TimeoutMS      int      `json:"timeoutMs,omitempty"`
	Actions        []Action `json:"actions,omitempty"`
	AllowedOrigins []string `json:"allowedOrigins,omitempty"`
	FollowNewTabs  bool     `json:"followNewTabs,omitempty"`
	Checks
}
type SelectOption struct {
	Index    int    `json:"index"`
	Label    string `json:"label"`
	Value    string `json:"value"`
	Selected bool   `json:"selected"`
	Disabled bool   `json:"disabled"`
}
type Node struct {
	Ref              string         `json:"nodeRef"`
	Role             string         `json:"role"`
	Name             string         `json:"name"`
	Context          string         `json:"context"`
	Href             string         `json:"href"`
	Value            *string        `json:"value"`
	InputType        string         `json:"inputType"`
	Editable         bool           `json:"editable"`
	Disabled         bool           `json:"disabled"`
	Ignored          bool           `json:"ignored"`
	Visible          bool           `json:"visible"`
	Checked          any            `json:"checked,omitempty"`
	Selected         any            `json:"selected,omitempty"`
	Expanded         any            `json:"expanded,omitempty"`
	Options          []SelectOption `json:"options,omitempty"`
	OptionsTruncated bool           `json:"optionsTruncated,omitempty"`
	OpensNewTab      bool           `json:"opensNewTab,omitempty"`
}
type Snapshot struct {
	ID             string  `json:"snapshotId"`
	Version        int     `json:"metadataVersion"`
	URL            string  `json:"url"`
	Nodes          []Node  `json:"nodes"`
	DocumentKey    string  `json:"documentKey,omitempty"`
	Title          string  `json:"title,omitempty"`
	Text           string  `json:"text,omitempty"`
	ReadyState     string  `json:"readyState,omitempty"`
	ScrollY        float64 `json:"scrollY,omitempty"`
	ScrollMax      float64 `json:"scrollMax,omitempty"`
	ViewportHeight float64 `json:"viewportHeight,omitempty"`
}

type Step struct {
	Index       int    `json:"index"`
	Action      string `json:"action"`
	Target      string `json:"target,omitempty"`
	MatchedName string `json:"matched_name,omitempty"`
	Selection   string `json:"selection,omitempty"`
	Verified    bool   `json:"verified"`
	Execution   string `json:"execution,omitempty"`
	Note        string `json:"note,omitempty"`
	TabID       int    `json:"tabId,omitempty"`
}
type Metrics struct {
	BrowserCalls     int   `json:"browser_calls"`
	DurationMS       int64 `json:"duration_ms"`
	ReadRecoveries   int   `json:"read_recoveries"`
	ActionRecoveries int   `json:"action_recoveries"`
}
type Result struct {
	Status           string  `json:"status"`
	TabID            int     `json:"tabId,omitempty"`
	NextTabID        int     `json:"nextTabId,omitempty"`
	Outcome          string  `json:"outcome,omitempty"`
	Code             string  `json:"code,omitempty"`
	Message          string  `json:"message,omitempty"`
	URL              string  `json:"url,omitempty"`
	Steps            []Step  `json:"steps"`
	CompletedActions int     `json:"completedActions"`
	NextAction       int     `json:"nextAction"`
	Metrics          Metrics `json:"metrics"`
}
type CallFunc func(context.Context, string, any) (protocol.Response, error)
type Runner struct{ Call CallFunc }
type failure struct{ code, message string }

func (e *failure) Error() string      { return e.message }
func fail(code, message string) error { return &failure{code, message} }

func ParseRequest(params any) (Request, error) {
	var req Request
	raw, err := json.Marshal(params)
	if err != nil || len(raw) > 32*1024 {
		return req, errors.New("browser.run requires an object of at most 32 KiB")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&req) != nil {
		return req, errors.New("invalid browser.run fields")
	}
	// Empty input is allowed only when explicitly supplied by the caller.
	var object struct {
		Fields  []map[string]json.RawMessage `json:"fields"`
		Verify  []map[string]json.RawMessage `json:"verifyFields"`
		Actions []struct {
			Verify []map[string]json.RawMessage `json:"verifyFields"`
		} `json:"actions"`
	}
	if json.Unmarshal(raw, &object) != nil {
		return req, errors.New("invalid fields")
	}
	fields := append(object.Fields, object.Verify...)
	for _, a := range object.Actions {
		fields = append(fields, a.Verify...)
	}
	for _, f := range fields {
		v, ok := f["text"]
		if !ok || len(v) == 0 || v[0] != '"' {
			return req, errors.New("each field must explicitly supply text")
		}
	}
	return req, req.validate()
}
func (c Checks) configured() bool {
	return c.ExpectText != "" || c.ExpectURL != "" || len(c.VerifyFields) > 0
}
func (c Checks) validate() error {
	if len(c.ExpectText) > 1024 || len(c.ExpectURL) > 4096 || len(c.VerifyFields) > 10 {
		return errors.New("verification limits exceeded")
	}
	if c.ExpectURL != "" && origin(c.ExpectURL) == "" {
		return errors.New("expectURL must be an HTTP(S) URL")
	}
	for _, f := range c.VerifyFields {
		if strings.TrimSpace(f.Target) == "" || len(f.Target) > 512 || len(f.Text) > 4096 {
			return errors.New("invalid field verification")
		}
	}
	return nil
}
func (r Request) validate() error {
	if r.ProfileID == "" || r.TabID <= 0 {
		return errors.New("profileId and tabId are required")
	}
	if r.TimeoutMS != 0 && (r.TimeoutMS < 100 || r.TimeoutMS > 60000) {
		return errors.New("timeoutMs must be between 100 and 60000")
	}
	if len(r.Target) > 512 || len(r.EmptyText) > 1024 {
		return errors.New("target or verification text is too long")
	}
	if err := r.Checks.validate(); err != nil {
		return err
	}
	if len(r.AllowedOrigins) > 8 {
		return errors.New("at most 8 allowedOrigins")
	}
	for _, v := range r.AllowedOrigins {
		if origin(v) == "" || strings.TrimRight(v, "/") != origin(v) {
			return errors.New("allowedOrigins must be exact HTTP(S) origins")
		}
	}
	if r.Workflow != "sequence" && (len(r.Actions) > 0 || len(r.AllowedOrigins) > 0 || r.FollowNewTabs) {
		return errors.New("actions and navigation options require workflow=sequence")
	}
	switch r.Workflow {
	case "sequence":
		if len(r.Actions) == 0 || len(r.Actions) > 60 || r.Target != "" || r.Text != "" || len(r.Fields) > 0 || r.PressEnter || r.EmptyText != "" {
			return errors.New("sequence requires 1 to 60 explicit actions")
		}
		for _, a := range r.Actions {
			if err := a.validate(); err != nil {
				return err
			}
		}
	case "search":
		if strings.TrimSpace(r.Target) == "" || r.Text == "" || len(r.Text) > 4096 || len(r.Fields) != 0 {
			return errors.New("search requires target and text, without fields")
		}
		if !r.Checks.configured() && r.EmptyText == "" {
			return errors.New("search requires an explicit result condition")
		}
	case "navigate":
		if strings.TrimSpace(r.Target) == "" || r.Text != "" || len(r.Fields) != 0 || r.PressEnter || r.EmptyText != "" {
			return errors.New("navigate requires target and optional result checks")
		}
	case "fill":
		if len(r.Fields) == 0 || len(r.Fields) > 10 || r.Target != "" || r.Text != "" || r.PressEnter || r.EmptyText != "" {
			return errors.New("fill requires 1 to 10 fields and does not submit")
		}
		seen := map[string]bool{}
		for _, f := range r.Fields {
			k := normalize(f.Target)
			if k == "" || len(f.Target) > 512 || len(f.Text) > 4096 || seen[k] {
				return errors.New("fill targets must be nonempty, unique and bounded")
			}
			seen[k] = true
		}
	default:
		return errors.New("workflow must be search, navigate, fill or sequence")
	}
	return nil
}
func (a Action) validate() error {
	if len(a.Target) > 512 || len(a.Role) > 64 || len(a.Href) > 4096 || a.TimeoutMS < 0 || a.TimeoutMS > 10000 {
		return errors.New("invalid action limits")
	}
	if a.Href != "" && origin(a.Href) == "" {
		return errors.New("href must be an HTTP(S) URL")
	}
	if err := a.Checks.validate(); err != nil {
		return err
	}
	if a.Type != "wait" && a.Type != "assert" && a.Checks.configured() {
		return errors.New("action checks require wait or assert")
	}
	if a.Type != "type" && a.Text != nil || a.Type != "select" && a.Option != nil || a.Type != "key" && a.Key != "" || a.Type != "scroll" && a.DeltaY != nil {
		return errors.New("action parameters do not match type")
	}
	switch a.Type {
	case "click", "type", "select", "key":
		if strings.TrimSpace(a.Target) == "" && a.Role == "" && a.Href == "" {
			return errors.New("action requires an exact target, role or href")
		}
		if a.Type == "type" && (a.Text == nil || len(*a.Text) > 4096) {
			return errors.New("type requires explicit text of at most 4096 bytes")
		}
		if a.Type == "select" && (a.Option == nil || len(*a.Option) > 4096) {
			return errors.New("select requires an explicit option label or value")
		}
		if a.Type == "key" {
			switch a.Key {
			case "Enter", "Tab", "Escape", "Backspace", "ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight":
			default:
				return errors.New("unsupported key")
			}
		}
	case "scroll":
		if a.DeltaY == nil || math.IsNaN(*a.DeltaY) || math.IsInf(*a.DeltaY, 0) || math.Abs(*a.DeltaY) > 100000 {
			return errors.New("scroll requires a finite bounded deltaY")
		}
	case "wait", "assert":
		if !a.Checks.configured() {
			return errors.New("wait/assert require an explicit condition")
		}
	default:
		return errors.New("unsupported action type")
	}
	if (a.Type == "scroll" || a.Type == "wait" || a.Type == "assert") && (a.Target != "" || a.Role != "" || a.Href != "") {
		return errors.New("this action does not take a target")
	}
	return nil
}
func normalize(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }

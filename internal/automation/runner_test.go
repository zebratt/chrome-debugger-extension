package automation

import (
	"chrome-connector/internal/protocol"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func str(s string) *string { return &s }
func input(name, value string) Node {
	return Node{Role: "textbox", Name: name, Editable: true, InputType: "text", Value: str(value), Visible: true}
}

type scene struct {
	url, text, ready                                              string
	nodes                                                         []Node
	refs                                                          map[string]int
	reads, writes, types, clicks, selects, keys, claims, releases int
	preStale, postStale, readErrors                               int
	failWrite                                                     string
	unverified, busy, child, opened                               bool
	afterClick                                                    func(*scene, Node)
	afterRead                                                     func(*scene)
}

func newScene(nodes ...Node) *scene {
	return &scene{url: "https://example.test/start", ready: "complete", nodes: nodes}
}
func (b *scene) call(_ context.Context, method string, params any) (protocol.Response, error) {
	p := params.(map[string]any)
	value := any(map[string]any{})
	switch method {
	case "tab.info":
		u := b.url
		if p["tabId"] == 2 {
			u = "https://example.test/child"
		}
		value = map[string]any{"url": u}
	case "tab.list":
		tabs := []any{map[string]any{"tabId": 1, "url": b.url}}
		if b.opened {
			tabs = append(tabs, map[string]any{"tabId": 2, "openerTabId": 1, "url": "https://example.test/child"})
		}
		value = map[string]any{"tabs": tabs}
	case "tab.snapshot":
		b.reads++
		if b.readErrors > 0 {
			b.readErrors--
			return protocol.ErrorResponse(nil, "STALE_SNAPSHOT", "loading", true), nil
		}
		if b.afterRead != nil {
			b.afterRead(b)
		}
		b.refs = map[string]int{}
		nodes := append([]Node{}, b.nodes...)
		for i := range nodes {
			nodes[i].Ref = fmt.Sprintf("%d:%d", b.reads, i)
			b.refs[nodes[i].Ref] = i
		}
		u := b.url
		if p["tabId"] == 2 {
			u = "https://example.test/child"
		}
		value = Snapshot{ID: fmt.Sprint(b.reads), Version: 2, DocumentKey: u, URL: u, ReadyState: b.ready, Text: b.text, Nodes: nodes}
	case "tab.claim":
		if b.busy {
			return protocol.ErrorResponse(nil, "TAB_BUSY", "busy", false), nil
		}
		b.claims++
		value = map[string]any{"leaseToken": "lease"}
	case "tab.release":
		b.releases++
	case "tab.renew":
		if p["leaseToken"] != "lease" {
			return protocol.ErrorResponse(nil, "LEASE_EXPIRED", "bad lease", false), nil
		}
	case "tab.type", "tab.click", "tab.select", "tab.key", "tab.scroll":
		if b.preStale > 0 {
			b.preStale--
			return protocol.ErrorResponse(nil, "STALE_SNAPSHOT", "target changed", true), nil
		}
		if p["snapshotId"] != fmt.Sprint(b.reads) {
			return protocol.ErrorResponse(nil, "STALE_SNAPSHOT", "old snapshot", true), nil
		}
		b.writes++
		if method != "tab.scroll" {
			if p["guarded"] != true {
				return protocol.Response{}, fmt.Errorf("missing target guard")
			}
			i, ok := b.refs[p["nodeRef"].(string)]
			if !ok {
				return protocol.Response{}, fmt.Errorf("unknown target")
			}
			n := b.nodes[i]
			switch method {
			case "tab.type":
				b.types++
				b.nodes[i].Value = str(p["text"].(string))
				value = map[string]any{"verified": !b.unverified}
			case "tab.select":
				b.selects++
				for j := range b.nodes[i].Options {
					b.nodes[i].Options[j].Selected = b.nodes[i].Options[j].Index == p["optionIndex"].(int)
				}
				value = map[string]any{"verified": !b.unverified}
			case "tab.key":
				b.keys++
				b.text = "Search completed"
			case "tab.click":
				b.clicks++
				if b.child {
					b.opened = true
				}
				if n.Role == "link" {
					b.url = n.Href
				}
				if n.Role == "checkbox" {
					b.nodes[i].Checked = true
				}
				if b.afterClick != nil {
					b.afterClick(b, n)
				}
			}
		}
		b.readErrors = b.postStale
		b.postStale = 0
		if b.failWrite != "" {
			return protocol.ErrorResponse(nil, b.failWrite, "response lost", false), nil
		}
	default:
		return protocol.Response{}, fmt.Errorf("unexpected method %s", method)
	}
	return protocol.Response{Result: value}, nil
}
func run(b *scene, r Request) Result {
	r.ProfileID = "p"
	r.TabID = 1
	if r.Workflow == "" {
		r.Workflow = "sequence"
	}
	return (Runner{Call: b.call}).Run(context.Background(), r)
}
func TestSequenceReobservesAndVerifiesAllFields(t *testing.T) {
	b := newScene(input("City", ""), Node{Role: "combobox", Name: "Style", InputType: "select", Options: []SelectOption{{Index: 0, Value: "any", Label: "Any", Selected: true}, {Index: 1, Value: "design", Label: "Design"}}}, Node{Role: "checkbox", Name: "Free cancellation", Checked: false}, Node{Role: "button", Name: "Search"})
	b.afterClick = func(b *scene, n Node) {
		if n.Name == "Search" {
			b.text = "Matching hotels"
		}
	}
	got := run(b, Request{Actions: []Action{{Type: "type", Target: "City", Text: str("Lisbon")}, {Type: "select", Target: "Style", Option: str("Design")}, {Type: "click", Target: "Free cancellation"}, {Type: "click", Target: "Search"}}, Checks: Checks{ExpectText: "Matching hotels", VerifyFields: []Field{{"City", "Lisbon"}, {"Style", "Design"}, {"Free cancellation", "true"}}}})
	if got.Status != "completed" || got.Outcome != "verified" || got.CompletedActions != 4 || got.NextAction != 4 || b.types != 1 || b.selects != 1 || b.clicks != 2 {
		t.Fatalf("%+v", got)
	}
}
func TestSequenceTargetsNewStateInsteadOfReusingNodes(t *testing.T) {
	b := newScene(Node{Role: "button", Name: "First"})
	b.afterClick = func(b *scene, _ Node) {
		b.nodes = []Node{{Role: "button", Name: fmt.Sprint(b.clicks)}}
		if b.clicks == 3 {
			b.text = "Won"
		}
	}
	got := run(b, Request{Actions: []Action{{Type: "click", Role: "button"}, {Type: "click", Role: "button"}, {Type: "click", Role: "button"}}, Checks: Checks{ExpectText: "Won"}})
	if got.Status != "completed" || b.clicks != 3 || b.reads < 4 {
		t.Fatalf("%+v", got)
	}
}
func TestTextTargetsMustExplicitlyNameTheObservedRole(t *testing.T) {
	snap := Snapshot{Nodes: []Node{{Ref: "a", Role: "StaticText", Name: "this does nothing"}}}
	if _, err := findTarget(snap, Action{Type: "click", Target: "this does nothing"}); err == nil {
		t.Fatal("implicit text click")
	}
	if _, err := findTarget(snap, Action{Type: "click", Target: "this does nothing", Role: "StaticText"}); err != nil {
		t.Fatal(err)
	}
}
func TestAmbiguousTargetsDoNotWrite(t *testing.T) {
	b := newScene(input("Name", ""), input("Name", ""))
	got := run(b, Request{Actions: []Action{{Type: "type", Target: "Name", Text: str("Matt")}}})
	if got.Code != "AMBIGUOUS_TARGET" || b.writes != 0 {
		t.Fatalf("%+v", got)
	}
}
func TestStaleBeforeActionRecoversOnlyTheUnexecutedStep(t *testing.T) {
	b := newScene(input("Name", ""))
	b.preStale = 1
	got := run(b, Request{Actions: []Action{{Type: "type", Target: "Name", Text: str("Matt")}}})
	if got.Status != "completed" || b.types != 1 || got.Metrics.ActionRecoveries != 1 || got.Steps[0].Execution != "not_executed" {
		t.Fatalf("%+v", got)
	}
}
func TestReadRecoveryKeepsCommittedPrefix(t *testing.T) {
	for _, errors := range []int{2, 5} {
		t.Run(fmt.Sprint(errors), func(t *testing.T) {
			b := newScene(input("Name", ""), input("Email", ""))
			b.postStale = errors
			got := run(b, Request{Actions: []Action{{Type: "type", Target: "Name", Text: str("Matt")}, {Type: "type", Target: "Email", Text: str("m@example.test")}}})
			if errors == 2 {
				if got.Status != "completed" || b.types != 2 || got.Metrics.ReadRecoveries != 2 {
					t.Fatalf("%+v", got)
				}
			} else if got.Status != "needs_attention" || b.types != 1 || got.NextAction != 1 || got.CompletedActions != 1 || got.Steps[0].Execution != "executed" {
				t.Fatalf("%+v", got)
			}
		})
	}
}
func TestUnknownAndUnverifiedActionsNeverReplayOrAdvance(t *testing.T) {
	for _, code := range []string{"OUTCOME_UNKNOWN", "INPUT_UNVERIFIED"} {
		t.Run(code, func(t *testing.T) {
			b := newScene(input("Search", ""))
			if code == "INPUT_UNVERIFIED" {
				b.unverified = true
			} else {
				b.failWrite = code
			}
			got := run(b, Request{Actions: []Action{{Type: "type", Target: "Search", Text: str("hello")}, {Type: "key", Target: "Search", Key: "Enter"}}})
			if got.Code != code || b.types != 1 || b.keys != 0 || got.NextAction != 0 {
				t.Fatalf("%+v", got)
			}
		})
	}
}
func TestExactURLAndAllChecksAreRequired(t *testing.T) {
	for _, u := range []string{"https://example.test/path/?q=value#part", "https://example.test/path?q=value/#part", "https://example.test/path?q=value#part/"} {
		if (Checks{ExpectURL: "https://example.test/path?q=value#part"}).matches(Snapshot{Version: 2, ReadyState: "complete", URL: u}) {
			t.Fatal("URL differences discarded")
		}
	}
	b := newScene(input("Name", ""))
	b.text = "Done"
	got := run(b, Request{TimeoutMS: 100, Actions: []Action{{Type: "type", Target: "Name", Text: str("Matt")}}, Checks: Checks{ExpectText: "Done", VerifyFields: []Field{{"Name", "Wrong"}}}})
	if got.Status != "needs_attention" || b.types != 1 || got.NextAction != 1 {
		t.Fatalf("%+v", got)
	}
}
func TestWaitAssertAndDelayedTarget(t *testing.T) {
	b := newScene()
	b.afterRead = func(b *scene) {
		if b.reads >= 3 {
			b.nodes = []Node{{Role: "button", Name: "Ready"}}
			b.text = "Ready"
		}
	}
	got := run(b, Request{Actions: []Action{{Type: "click", Target: "Ready"}, {Type: "wait", Checks: Checks{ExpectText: "Ready"}}, {Type: "assert", Checks: Checks{ExpectText: "Ready"}}}})
	if got.Status != "completed" || b.clicks != 1 || got.CompletedActions != 3 {
		t.Fatalf("%+v", got)
	}
}
func TestParsedStableDocumentCanContinueWithoutComplete(t *testing.T) {
	b := newScene(input("Name", ""))
	b.ready = "loading"
	b.afterRead = func(b *scene) {
		if b.reads > 2 {
			b.ready = "interactive"
		}
	}
	got := run(b, Request{Actions: []Action{{Type: "type", Target: "Name", Text: str("Matt")}}, Checks: Checks{VerifyFields: []Field{{"Name", "Matt"}}}})
	if got.Status != "completed" || b.types != 1 || b.reads < 4 {
		t.Fatalf("%+v", got)
	}
}

func TestLateTargetIsNotReturnedFromALoadingDocument(t *testing.T) {
	b := newScene()
	b.afterRead = func(b *scene) {
		b.nodes = []Node{input("Late field", "")}
		b.ready = "loading"
		if b.reads >= 2 {
			b.ready = "complete"
		}
	}
	s := &session{runner: Runner{Call: b.call}, req: Request{ProfileID: "p", TabID: 1, Workflow: "sequence"}}
	snap, node, err := s.locate(context.Background(), Snapshot{Version: 2, ReadyState: "complete"}, Action{Type: "type", Target: "Late field"})
	if err != nil || snap.ReadyState != "complete" || node.Name != "Late field" || b.reads < 2 {
		t.Fatalf("snap=%+v node=%+v err=%v", snap, node, err)
	}
}
func TestNewChildTabRequiresOptInAndOwnLease(t *testing.T) {
	for _, follow := range []bool{false, true} {
		b := newScene(Node{Role: "button", Name: "Open"})
		b.child = true
		got := run(b, Request{Actions: []Action{{Type: "click", Target: "Open"}}, FollowNewTabs: follow, Checks: Checks{ExpectURL: "https://example.test/child"}})
		if follow {
			if got.Status != "completed" || got.TabID != 2 || b.releases != 2 {
				t.Fatalf("%+v", got)
			}
		} else if got.Code != "NEW_TAB_OPENED" || got.NextTabID != 2 || got.NextAction != 1 || b.releases != 1 {
			t.Fatalf("%+v", got)
		}
	}
}
func TestBorrowedClaimIsNotReleasedAndBusyTabIsNotTaken(t *testing.T) {
	b := newScene(input("Name", ""))
	got := run(b, Request{LeaseToken: "lease", Actions: []Action{{Type: "type", Target: "Name", Text: str("Matt")}}})
	if got.Status != "completed" || b.releases != 0 {
		t.Fatalf("%+v", got)
	}
	b = newScene(input("Name", ""))
	b.busy = true
	got = run(b, Request{Actions: []Action{{Type: "type", Target: "Name", Text: str("Matt")}}})
	if got.Code != "TAB_BUSY" || b.writes != 0 {
		t.Fatalf("%+v", got)
	}
}
func TestFixedFillSearchAndNavigate(t *testing.T) {
	b := newScene(input("Name", "Matt"))
	got := run(b, Request{Workflow: "fill", Fields: []Field{{"Name", "Matt"}}})
	if got.Outcome != "filled" || b.writes != 0 {
		t.Fatalf("%+v", got)
	}
	b = newScene(input("Search", ""))
	got = run(b, Request{Workflow: "search", Target: "Search", Text: "hello", PressEnter: true, Checks: Checks{ExpectText: "Search completed"}})
	if got.Status != "completed" || b.keys != 1 {
		t.Fatalf("%+v", got)
	}
	b = newScene(Node{Role: "link", Name: "Guide", Href: "https://example.test/guide"})
	got = run(b, Request{Workflow: "navigate", Target: "Guide"})
	if got.Status != "completed" || got.URL != "https://example.test/guide" {
		t.Fatalf("%+v", got)
	}
}
func TestUnchangedSearchAndProtectedInputCannotSucceed(t *testing.T) {
	b := newScene(input("Search", ""))
	b.text = "Search completed"
	got := run(b, Request{Workflow: "search", Target: "Search", Text: "new", PressEnter: true, TimeoutMS: 100, Checks: Checks{ExpectText: "Search completed"}})
	if got.Code != "TIMEOUT" {
		t.Fatalf("%+v", got)
	}
	for _, kind := range []string{"password", "file", "hidden"} {
		b = newScene(input("Secret", ""))
		b.nodes[0].InputType = kind
		got = run(b, Request{Actions: []Action{{Type: "type", Target: "Secret", Text: str("x")}}})
		if got.Code != "PROTECTED_TARGET" || b.writes != 0 {
			t.Fatalf("%+v", got)
		}
	}
}
func TestOriginBoundariesAndInterruptedInitialObservation(t *testing.T) {
	b := newScene(Node{Role: "link", Name: "Go", Href: "https://other.test/"})
	got := run(b, Request{Actions: []Action{{Type: "click", Target: "Go"}}})
	if got.Code != "ORIGIN_CHANGE_REQUIRED" || b.clicks != 0 {
		t.Fatalf("%+v", got)
	}
	b = newScene(input("Name", ""))
	reads := 0
	call := func(ctx context.Context, m string, p any) (protocol.Response, error) {
		if m == "tab.info" && reads == 0 {
			return protocol.Response{Result: map[string]any{"url": "https://old.test/"}}, nil
		}
		if m == "tab.snapshot" && reads == 0 {
			reads++
			return protocol.ErrorResponse(nil, "STALE_SNAPSHOT", "navigation", true), nil
		}
		return b.call(ctx, m, p)
	}
	got = (Runner{Call: call}).Run(context.Background(), Request{ProfileID: "p", TabID: 1, Workflow: "sequence", Actions: []Action{{Type: "type", Target: "Name", Text: str("Matt")}}})
	if got.Status != "completed" || got.Metrics.ReadRecoveries != 1 {
		t.Fatalf("%+v", got)
	}
}
func TestRequestRejectsMissingInputAndInvalidActionParameters(t *testing.T) {
	for _, tail := range []string{`"actions":[{"type":"type","target":"Name"}]`, `"actions":[{"type":"click","target":"Go","text":"ignored"}]`, `"actions":[{"type":"assert"}]`, `"actions":[{"type":"type","target":"Name","text":"x"}],"engine":"remote"`, `"actions":[{"type":"assert","verifyFields":[{"target":"Name"}]}]`} {
		var v any
		json.Unmarshal([]byte(`{"profileId":"p","tabId":1,"workflow":"sequence",`+tail+`}`), &v)
		if _, err := ParseRequest(v); err == nil {
			t.Fatalf("accepted %s", tail)
		}
	}
	var v any
	json.Unmarshal([]byte(`{"profileId":"p","tabId":1,"workflow":"sequence","actions":[{"type":"type","target":"Name","text":""}]}`), &v)
	if _, err := ParseRequest(v); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(InputSchema())
	if strings.Contains(string(raw), `"engine"`) {
		t.Fatal("unexpected provider configuration")
	}
}

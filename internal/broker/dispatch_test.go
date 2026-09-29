package broker

import (
	"encoding/json"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"chrome-connector/internal/policy"
	"chrome-connector/internal/protocol"
)

func request(method string, params any) protocol.Request {
	encoded, _ := json.Marshal(params)
	return protocol.Request{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: method, Params: encoded}
}

func TestTabOpenRejectsRestrictedURLBeforeForwarding(t *testing.T) {
	server := NewServer(policy.Default(), time.Minute)
	response := server.dispatch(request("tab.open", map[string]any{"profileId": "profile-a", "url": "file:///tmp/secret"}))
	if response.Error == nil || response.Error.Data.Kind != "POLICY_DENIED" {
		t.Fatalf("response %+v", response)
	}
}

func TestTabNavigateRequiresAValidLease(t *testing.T) {
	server := NewServer(policy.Default(), time.Minute)
	response := server.dispatch(request("tab.navigate", map[string]any{"profileId": "profile-a", "tabId": 7, "url": "https://example.com"}))
	if response.Error == nil || response.Error.Data.Kind != "LEASE_EXPIRED" {
		t.Fatalf("response %+v", response)
	}
}

func TestRawCDPIsDisabledByDefault(t *testing.T) {
	server := NewServer(policy.Default(), time.Minute)
	response := server.dispatch(request("cdp.send", map[string]any{"profileId": "profile-a", "tabId": 7, "method": "Network.enable"}))
	if response.Error == nil || response.Error.Data.Kind != "POLICY_DENIED" {
		t.Fatalf("response %+v", response)
	}
}

func TestTabClaimReturnsReusableToken(t *testing.T) {
	server := NewServer(policy.Default(), time.Minute)
	claimed := server.dispatch(request("tab.claim", map[string]any{"profileId": "profile-a", "tabId": 7}))
	if claimed.Error != nil {
		t.Fatalf("claim %+v", claimed.Error)
	}
	lease := claimed.Result.(map[string]any)
	token := lease["leaseToken"].(string)
	renewed := server.dispatch(request("tab.renew", map[string]any{"profileId": "profile-a", "tabId": 7, "leaseToken": token}))
	if renewed.Error != nil || renewed.Result.(map[string]any)["leaseToken"] != token {
		t.Fatalf("renewed %+v", renewed)
	}
}

func TestPageMethodsRequireAProfileAndLeaseForWrites(t *testing.T) {
	server := NewServer(policy.Default(), time.Minute)
	read := server.dispatch(request("tab.snapshot", map[string]any{"profileId": "profile-a", "tabId": 7}))
	if read.Error == nil || read.Error.Data.Kind != "BROWSER_OFFLINE" {
		t.Fatalf("snapshot %+v", read)
	}
	write := server.dispatch(request("tab.click", map[string]any{"profileId": "profile-a", "tabId": 7, "snapshotId": "s", "nodeRef": "n"}))
	if write.Error == nil || write.Error.Data.Kind != "LEASE_EXPIRED" {
		t.Fatalf("click %+v", write)
	}
}

func TestPolicyRPCIsReadOnlySnapshot(t *testing.T) {
	active := policy.Policy{BlockedSites: []string{"secret.example.com"}}
	server := NewServer(active, time.Minute)
	response := server.dispatch(request("system.policy", nil))
	if response.Error != nil {
		t.Fatalf("policy RPC error %+v", response.Error)
	}
	policyResult, ok := response.Result.(policy.Policy)
	if !ok || len(policyResult.BlockedSites) != 1 || policyResult.RawCDP {
		t.Fatalf("policy RPC result %+v", response.Result)
	}
}

func TestCapabilitiesShowCDPDefaultAndProtocolVersion(t *testing.T) {
	server := NewServer(policy.Default(), time.Minute)
	response := server.dispatch(request("system.capabilities", nil))
	if response.Error != nil {
		t.Fatalf("capabilities error %+v", response.Error)
	}
	result, ok := response.Result.(map[string]any)
	if !ok || result["protocolVersion"] != "1.0" || result["rawCdpEnabled"] != false {
		t.Fatalf("capabilities %+v", response.Result)
	}
	methods, ok := result["methods"].([]string)
	if !ok || len(methods) < 18 || result["brokerVersion"] == "" {
		t.Fatalf("public methods or broker version missing: %+v", result)
	}
}

func TestCapabilitiesIntersectPolicyAndChromeDomains(t *testing.T) {
	server := NewServer(policy.Policy{RawCDP: true, CDPDomains: []string{"Page", "Browser"}}, time.Minute)
	server.hosts["p"] = &hostSession{profileID: "p", version: "1.0", hostVersion: "0.1.0", extensionVersion: "0.1.0", supportedCDPDomains: []string{"Page", "Runtime"}}
	response := server.dispatch(request("system.capabilities", nil))
	result := response.Result.(map[string]any)
	profile := result["profiles"].([]map[string]any)[0]
	effective := profile["effectiveCdpDomains"].([]string)
	if len(effective) != 1 || effective[0] != "Page" || profile["hostVersion"] != "0.1.0" {
		t.Fatalf("capabilities %+v", result)
	}
}

func TestMissingExtensionProtocolVersionIsReportedAsMismatch(t *testing.T) {
	server := NewServer(policy.Default(), time.Minute)
	server.rejected["p"] = rejectedHost{protocolVersion: ""}
	response := server.forward("p", request("tab.list", map[string]any{"profileId": "p"}))
	if response.Error == nil || response.Error.Data.Kind != "VERSION_MISMATCH" {
		t.Fatalf("missing protocol version %+v", response)
	}
}

func TestSentClickWithLostHostResponseIsOutcomeUnknown(t *testing.T) {
	server := NewServer(policy.Default(), time.Minute)
	brokerEnd, hostEnd := net.Pipe()
	defer hostEnd.Close()
	host := &hostSession{
		profileID: "profile-a", conn: brokerEnd, encoder: json.NewEncoder(brokerEnd),
		pending: make(map[string]chan protocol.Response), done: make(chan struct{}), closeOnce: sync.Once{},
	}
	server.hosts["profile-a"] = host
	go func() {
		var received map[string]any
		json.NewDecoder(hostEnd).Decode(&received)
		host.close()
	}()
	response := server.forward("profile-a", request("tab.click", map[string]any{"profileId": "profile-a", "tabId": 7}))
	if response.Error == nil || response.Error.Data.Kind != "OUTCOME_UNKNOWN" {
		t.Fatalf("response after lost result %+v", response)
	}
}

func TestOversizeCommandIsRejectedBeforeReachingNativeHost(t *testing.T) {
	server := NewServer(policy.Default(), time.Minute)
	response := server.dispatch(request("tab.type", map[string]any{"profileId": "profile-a", "tabId": 7, "text": strings.Repeat("x", 600*1024)}))
	if response.Error == nil || response.Error.Data.Kind != "PAYLOAD_TOO_LARGE" {
		t.Fatalf("oversize response %+v", response)
	}
}

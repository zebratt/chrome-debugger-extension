package broker

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"chrome-connector/internal/policy"
)

func TestBrokerRegistersHostAndForwardsTabList(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "cc-server-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(directory) })
	path := filepath.Join(directory, "broker.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	server := NewServer(policy.Policy{BlockedSites: []string{"secret.example.com"}}, time.Second)
	go server.Serve(ctx, listener)

	host, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	hostEncoder := json.NewEncoder(host)
	hostDecoder := json.NewDecoder(host)
	snapshotParams := make(chan map[string]any, 1)
	if err := hostEncoder.Encode(map[string]any{"kind": "hello", "profileId": "profile-a", "connectionId": "conn-a", "protocolVersion": "1.0", "extensionVersion": "0.1.0"}); err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			var request map[string]any
			if hostDecoder.Decode(&request) != nil {
				return
			}
			if request["method"] == "tab.click" {
				hostEncoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "error": map[string]any{"code": -32000, "message": "target detached", "data": map[string]any{"kind": "TARGET_DETACHED", "retryable": true}}})
				continue
			}
			result := map[string]any{"tabs": []any{
				map[string]any{"tabId": 7, "title": "Example", "url": "https://example.com"},
				map[string]any{"tabId": 9, "title": "Secret", "url": "https://secret.example.com"},
			}}
			switch request["method"] {
			case "tab.open":
				result = map[string]any{"tabId": 8}
			case "tab.info":
				result = map[string]any{"tabId": 7, "url": "https://example.com"}
			case "tab.snapshot":
				snapshotParams <- request["params"].(map[string]any)
				result = map[string]any{"snapshotId": "s", "nodes": []any{}}
			case "extension.reload":
				result = map[string]any{"reloading": true}
			}
			if hostEncoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": result}) != nil {
				return
			}
		}
	}()

	client, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.SetDeadline(time.Now().Add(5 * time.Second))
	encoder := json.NewEncoder(client)
	decoder := json.NewDecoder(client)
	if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": "a", "method": "browser.list"}); err != nil {
		t.Fatal(err)
	}
	var browsers struct {
		Result struct {
			Profiles []struct {
				ProfileID        string `json:"profileId"`
				ExtensionVersion string `json:"extensionVersion"`
				ConnectionID     string `json:"connectionId"`
			} `json:"profiles"`
		} `json:"result"`
	}
	if err := decoder.Decode(&browsers); err != nil {
		t.Fatal(err)
	}
	if len(browsers.Result.Profiles) != 1 || browsers.Result.Profiles[0].ProfileID != "profile-a" || browsers.Result.Profiles[0].ExtensionVersion != "0.1.0" || browsers.Result.Profiles[0].ConnectionID != "conn-a" {
		t.Fatalf("browser list %+v", browsers)
	}
	if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": "b", "method": "tab.list", "params": map[string]any{"profileId": "profile-a"}}); err != nil {
		t.Fatal(err)
	}
	var tabs struct {
		Result struct {
			Tabs []struct {
				TabID int `json:"tabId"`
			} `json:"tabs"`
		} `json:"result"`
	}
	if err := decoder.Decode(&tabs); err != nil {
		t.Fatal(err)
	}
	if len(tabs.Result.Tabs) != 1 || tabs.Result.Tabs[0].TabID != 7 {
		t.Fatalf("tabs %+v", tabs)
	}
	if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": "c", "method": "tab.open", "params": map[string]any{"profileId": "profile-a", "url": "https://example.com"}}); err != nil {
		t.Fatal(err)
	}
	var opened struct {
		Result struct {
			TabID      int    `json:"tabId"`
			LeaseToken string `json:"leaseToken"`
		} `json:"result"`
	}
	if err := decoder.Decode(&opened); err != nil {
		t.Fatal(err)
	}
	if opened.Result.TabID != 8 || opened.Result.LeaseToken == "" {
		t.Fatalf("opened tab missing lease %+v", opened)
	}
	if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": "d", "method": "tab.snapshot", "params": map[string]any{"profileId": "profile-a", "tabId": 7}}); err != nil {
		t.Fatal(err)
	}
	var snapshot map[string]any
	if err := decoder.Decode(&snapshot); err != nil {
		t.Fatal(err)
	}
	params := <-snapshotParams
	if params["expectedUrl"] != "https://example.com" {
		t.Fatalf("snapshot forwarded without checked URL: %+v", params)
	}
	if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": "claim", "method": "tab.claim", "params": map[string]any{"profileId": "profile-a", "tabId": 7}}); err != nil {
		t.Fatal(err)
	}
	var claim struct {
		Result struct {
			LeaseToken string `json:"leaseToken"`
		} `json:"result"`
	}
	if err := decoder.Decode(&claim); err != nil {
		t.Fatal(err)
	}
	if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": "click", "method": "tab.click", "params": map[string]any{"profileId": "profile-a", "tabId": 7, "leaseToken": claim.Result.LeaseToken, "snapshotId": "s", "nodeRef": "n"}}); err != nil {
		t.Fatal(err)
	}
	var clicked struct {
		Error struct {
			Data struct {
				Kind string `json:"kind"`
			} `json:"data"`
		} `json:"error"`
	}
	if err := decoder.Decode(&clicked); err != nil {
		t.Fatal(err)
	}
	if clicked.Error.Data.Kind != "TARGET_DETACHED" {
		t.Fatalf("click error %+v", clicked)
	}
	if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": "renew", "method": "tab.renew", "params": map[string]any{"profileId": "profile-a", "tabId": 7, "leaseToken": claim.Result.LeaseToken}}); err != nil {
		t.Fatal(err)
	}
	var renewal struct {
		Error struct {
			Data struct {
				Kind string `json:"kind"`
			} `json:"data"`
		} `json:"error"`
	}
	if err := decoder.Decode(&renewal); err != nil {
		t.Fatal(err)
	}
	if renewal.Error.Data.Kind != "LEASE_EXPIRED" {
		t.Fatalf("detached target kept lease %+v", renewal)
	}
	if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": "reload", "method": "extension.reload", "params": map[string]any{"profileId": "profile-a"}}); err != nil {
		t.Fatal(err)
	}
	var reloading struct {
		Result struct {
			Reloading bool `json:"reloading"`
		} `json:"result"`
	}
	if err := decoder.Decode(&reloading); err != nil {
		t.Fatal(err)
	}
	if !reloading.Result.Reloading {
		t.Fatalf("extension reload %+v", reloading)
	}
}

func TestBrokerExitsAfterIdleEvenWithHostConnected(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "cc-idle-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(directory) })
	listener, err := net.Listen("unix", filepath.Join(directory, "broker.sock"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := NewServer(policy.Default(), 100*time.Millisecond)
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, listener) }()
	host, err := net.Dial("unix", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	json.NewEncoder(host).Encode(map[string]any{"kind": "hello", "profileId": "profile-a", "protocolVersion": "1.0"})
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("broker stayed alive after idle timeout")
	}
}

func TestBrokerChecksCurrentURLBeforeReadingPage(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "cc-policy-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(directory) })
	path := filepath.Join(directory, "broker.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := NewServer(policy.Policy{BlockedSites: []string{"secret.example.com"}}, time.Second)
	go server.Serve(ctx, listener)
	host, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	hostEncoder, hostDecoder := json.NewEncoder(host), json.NewDecoder(host)
	hostEncoder.Encode(map[string]any{"kind": "hello", "profileId": "profile-a", "protocolVersion": "1.0"})
	called := make(chan string, 2)
	go func() {
		for {
			var request map[string]any
			if hostDecoder.Decode(&request) != nil {
				return
			}
			method := request["method"].(string)
			called <- method
			result := map[string]any{"url": "https://secret.example.com/", "tabId": 7}
			hostEncoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": result})
		}
	}()
	client, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.SetDeadline(time.Now().Add(5 * time.Second))
	json.NewEncoder(client).Encode(map[string]any{"jsonrpc": "2.0", "id": "policy", "method": "tab.snapshot", "params": map[string]any{"profileId": "profile-a", "tabId": 7}})
	var response struct {
		Error struct {
			Data struct {
				Kind string `json:"kind"`
			} `json:"data"`
		} `json:"error"`
	}
	if err := json.NewDecoder(client).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Error.Data.Kind != "POLICY_DENIED" {
		t.Fatalf("blocked URL response %+v", response)
	}
	select {
	case method := <-called:
		if method != "tab.info" {
			t.Fatalf("first host request %s, want tab.info", method)
		}
	default:
		t.Fatal("broker did not inspect current tab URL")
	}
	json.NewEncoder(client).Encode(map[string]any{"jsonrpc": "2.0", "id": "info", "method": "tab.info", "params": map[string]any{"profileId": "profile-a", "tabId": 7}})
	var infoResponse struct {
		Error struct {
			Data struct {
				Kind string `json:"kind"`
			} `json:"data"`
		} `json:"error"`
	}
	if err := json.NewDecoder(client).Decode(&infoResponse); err != nil {
		t.Fatal(err)
	}
	if infoResponse.Error.Data.Kind != "POLICY_DENIED" {
		t.Fatalf("blocked tab.info response %+v", infoResponse)
	}
}

func TestFirstProfileRequestWaitsForHostRegistration(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "cc-register-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(directory) })
	path := filepath.Join(directory, "broker.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := NewServer(policy.Default(), time.Second)
	go server.Serve(ctx, listener)
	go func() {
		time.Sleep(100 * time.Millisecond)
		host, err := net.Dial("unix", path)
		if err != nil {
			return
		}
		defer host.Close()
		encoder, decoder := json.NewEncoder(host), json.NewDecoder(host)
		encoder.Encode(map[string]any{"kind": "hello", "profileId": "profile-a", "protocolVersion": "1.0"})
		var request map[string]any
		if decoder.Decode(&request) == nil {
			encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": map[string]any{"tabs": []any{}}})
		}
	}()
	client, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.SetDeadline(time.Now().Add(5 * time.Second))
	json.NewEncoder(client).Encode(map[string]any{"jsonrpc": "2.0", "id": "early", "method": "tab.list", "params": map[string]any{"profileId": "profile-a"}})
	var response struct {
		Result map[string]any `json:"result"`
		Error  *struct {
			Data struct {
				Kind string `json:"kind"`
			} `json:"data"`
		} `json:"error"`
	}
	if err := json.NewDecoder(client).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Error != nil || response.Result == nil {
		t.Fatalf("early request failed %+v", response)
	}
}

func TestBrokerRoutesTwoProfilesIndependently(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "cc-profiles-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(directory) })
	path := filepath.Join(directory, "broker.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go NewServer(policy.Default(), time.Second).Serve(ctx, listener)
	register := func(profile string) net.Conn {
		conn, err := net.Dial("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		encoder, decoder := json.NewEncoder(conn), json.NewDecoder(conn)
		if err := encoder.Encode(map[string]any{"kind": "hello", "profileId": profile, "protocolVersion": "1.0"}); err != nil {
			t.Fatal(err)
		}
		go func() {
			for {
				var request map[string]any
				if decoder.Decode(&request) != nil {
					return
				}
				encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": map[string]any{"tabs": []any{map[string]any{"tabId": 7, "title": profile, "url": "https://example.com"}}}})
			}
		}()
		return conn
	}
	a, b := register("profile-a"), register("profile-b")
	defer a.Close()
	defer b.Close()
	client, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.SetDeadline(time.Now().Add(5 * time.Second))
	encoder, decoder := json.NewEncoder(client), json.NewDecoder(client)
	deadline := time.Now().Add(time.Second)
	for {
		encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": "profiles", "method": "browser.list"})
		var response struct {
			Result struct {
				Profiles []any `json:"profiles"`
			} `json:"result"`
		}
		if err := decoder.Decode(&response); err != nil {
			t.Fatal(err)
		}
		if len(response.Result.Profiles) == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("registered profiles %+v", response)
		}
		time.Sleep(10 * time.Millisecond)
	}
	encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": "b", "method": "tab.list", "params": map[string]any{"profileId": "profile-b"}})
	var tabs struct {
		Result struct {
			Tabs []struct {
				Title string `json:"title"`
			} `json:"tabs"`
		} `json:"result"`
	}
	if err := decoder.Decode(&tabs); err != nil {
		t.Fatal(err)
	}
	if len(tabs.Result.Tabs) != 1 || tabs.Result.Tabs[0].Title != "profile-b" {
		t.Fatalf("wrong profile route %+v", tabs)
	}
}

func TestShutdownAcknowledgesBeforeBrokerStops(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "cc-stop-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(directory) })
	path := filepath.Join(directory, "broker.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := NewServer(policy.Default(), time.Minute)
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, listener) }()
	client, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.SetDeadline(time.Now().Add(time.Second))
	json.NewEncoder(client).Encode(map[string]any{"jsonrpc": "2.0", "id": "stop", "method": "system.shutdown"})
	var response struct {
		Result struct {
			Stopping bool `json:"stopping"`
		} `json:"result"`
	}
	if err := json.NewDecoder(client).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if !response.Result.Stopping {
		t.Fatalf("shutdown response %+v", response)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("broker did not stop")
	}
}

func TestIncompatibleExtensionHelloIsReportedAsVersionMismatch(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "cc-version-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(directory) })
	path := filepath.Join(directory, "broker.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go NewServer(policy.Default(), time.Second).Serve(ctx, listener)
	host, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	json.NewEncoder(host).Encode(map[string]any{"kind": "hello", "profileId": "profile-a", "protocolVersion": "2.0", "extensionVersion": "2.0.0"})
	host.Close()
	client, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.SetDeadline(time.Now().Add(5 * time.Second))
	encoder, decoder := json.NewEncoder(client), json.NewDecoder(client)
	encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": "list", "method": "browser.list"})
	var listed struct {
		Result struct {
			DiscoveryState string `json:"discoveryState"`
			Profiles       []struct {
				Status          string `json:"status"`
				ProtocolVersion string `json:"protocolVersion"`
			} `json:"profiles"`
		} `json:"result"`
	}
	if err := decoder.Decode(&listed); err != nil {
		t.Fatal(err)
	}
	if listed.Result.DiscoveryState != "version_mismatch" || len(listed.Result.Profiles) != 1 || listed.Result.Profiles[0].Status != "version_mismatch" || listed.Result.Profiles[0].ProtocolVersion != "2.0" {
		t.Fatalf("version listing %+v", listed)
	}
	encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": "tabs", "method": "tab.list", "params": map[string]any{"profileId": "profile-a"}})
	var tabs struct {
		Error struct {
			Data struct {
				Kind string `json:"kind"`
			} `json:"data"`
		} `json:"error"`
	}
	if err := decoder.Decode(&tabs); err != nil {
		t.Fatal(err)
	}
	if tabs.Error.Data.Kind != "VERSION_MISMATCH" {
		t.Fatalf("version error %+v", tabs)
	}
}

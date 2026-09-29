package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"chrome-connector/internal/doctor"
	"chrome-connector/internal/protocol"
)

func TestCallCommandPrintsBrokerResult(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "cc-command-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(directory) })
	t.Setenv("CHROME_CONNECTOR_RUN_DIR", directory)
	listener, err := net.Listen("unix", filepath.Join(directory, "broker.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		conn.Close() // broker availability probe
		conn, err = listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var request map[string]any
		json.NewDecoder(conn).Decode(&request)
		json.NewEncoder(conn).Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": map[string]any{"status": "ready"}})
	}()
	var output, errors bytes.Buffer
	status := run([]string{"call", "system.ping"}, &output, &errors)
	if status != 0 {
		t.Fatalf("exit %d, stderr %s", status, errors.String())
	}
	if !strings.Contains(output.String(), `"status": "ready"`) {
		t.Fatalf("output %s", output.String())
	}
}

func TestChromeLaunchOriginSelectsNativeHostMode(t *testing.T) {
	got := normalizeArgs([]string{"chrome-extension://lmomiiblpebceaecbnlknkbnanhhdigi/"})
	if len(got) != 1 || got[0] != "native-host" {
		t.Fatalf("normalized args %+v", got)
	}
	got = normalizeArgs([]string{"call", "system.ping"})
	if len(got) != 2 || got[0] != "call" {
		t.Fatalf("explicit command changed %+v", got)
	}
}

func TestConvenienceCommandsMapToPublicMethods(t *testing.T) {
	for _, test := range []struct {
		args   []string
		method string
	}{
		{[]string{"browser", "list"}, "browser.list"},
		{[]string{"tab", "snapshot", `{"profileId":"p","tabId":1}`}, "tab.snapshot"},
		{[]string{"tab", "claim", `{"profileId":"p","tabId":1}`}, "tab.claim"},
		{[]string{"system", "capabilities"}, "system.capabilities"},
		{[]string{"cdp", "send", `{}`}, "cdp.send"},
	} {
		got := expandConvenienceArgs(test.args)
		if len(got) < 2 || got[0] != "call" || got[1] != test.method {
			t.Fatalf("%v mapped to %v", test.args, got)
		}
	}
	if got := expandConvenienceArgs([]string{"tab", "delete"}); got[0] != "tab" {
		t.Fatalf("unknown operation unexpectedly mapped: %v", got)
	}
}

func TestScreenshotOutputRequiresExplicitFile(t *testing.T) {
	if _, _, err := callArguments([]string{"call", "tab.screenshot", `{}`, "--output"}); err == nil {
		t.Fatal("missing output filename accepted")
	}
	if _, _, err := callArguments([]string{"call", "tab.list", `{}`, "--output", "/tmp/a.png"}); err == nil {
		t.Fatal("output accepted for non screenshot method")
	}
}

func TestSaveScreenshotWritesPrivateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "screen.png")
	response := protocol.Response{Result: map[string]any{"mimeType": "image/png", "dataBase64": base64.StdEncoding.EncodeToString([]byte("PNG"))}}
	if err := saveScreenshot(&response, path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("screenshot mode %v, err %v", info, err)
	}
	if _, exists := response.Result.(map[string]any)["dataBase64"]; exists {
		t.Fatal("image data remained in printed response")
	}
	if err := saveScreenshot(&protocol.Response{Result: map[string]any{"dataBase64": base64.StdEncoding.EncodeToString([]byte("PNG"))}}, path); err == nil {
		t.Fatal("existing screenshot was overwritten")
	}
}

func TestDoctorExplainsIncompatibleProfileWithoutCountingItOnline(t *testing.T) {
	report := doctor.Report{Issues: []string{}}
	annotateBrowserReport(&report, map[string]any{"discoveryState": "version_mismatch", "profiles": []any{map[string]any{"profileId": "p", "status": "version_mismatch", "protocolVersion": "2.0", "extensionVersion": "2.0.0"}}})
	if report.ConnectedProfiles != 0 || report.BrowserState != "version_mismatch" || len(report.Issues) != 1 || !strings.Contains(report.Issues[0], "reload or update") {
		t.Fatalf("doctor report %+v", report)
	}
}

func TestDoctorReportsActualBrokerAndHostVersions(t *testing.T) {
	report := doctor.Report{Issues: []string{}}
	annotateCapabilitiesReport(&report, map[string]any{"protocolVersion": "1.0", "brokerVersion": "0.1.0"})
	annotateBrowserReport(&report, map[string]any{"discoveryState": "ready", "profiles": []any{map[string]any{"profileId": "p", "status": "online", "protocolVersion": "1.0", "hostVersion": "0.1.0", "extensionVersion": "0.1.0"}}})
	if report.BrokerVersion != "0.1.0" || report.HostVersions["p"] != "0.1.0" || report.ConnectedProfiles != 1 || len(report.Issues) != 0 {
		t.Fatalf("doctor report %+v", report)
	}
}

func TestInstallReloadsConnectedExtensionAndWaitsForNewHostConnection(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "cc-reload-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(directory) })
	path := filepath.Join(directory, "broker.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	connectionID := "old"
	reloads := 0
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			var request map[string]any
			if json.NewDecoder(conn).Decode(&request) == nil {
				result := map[string]any{}
				if request["method"] == "browser.list" {
					result = map[string]any{"profiles": []any{map[string]any{"profileId": "profile-a", "connectionId": connectionID}}}
				}
				if request["method"] == "extension.reload" {
					reloads++
					connectionID = "new"
					result = map[string]any{"reloading": true}
				}
				json.NewEncoder(conn).Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": result})
			}
			conn.Close()
		}
	}()
	if err := reloadConnectedExtensions(path); err != nil {
		t.Fatal(err)
	}
	if reloads != 1 {
		t.Fatalf("reload requests %d", reloads)
	}
}

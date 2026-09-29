package main

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

package client

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCallSendsJSONRPCAndReturnsResponse(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "cc-client-")
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
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var request map[string]any
		json.NewDecoder(conn).Decode(&request)
		json.NewEncoder(conn).Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": map[string]any{"echoMethod": request["method"]}})
	}()
	response, err := Call(path, "system.ping", nil, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if response.Error != nil {
		t.Fatalf("unexpected RPC error %+v", response.Error)
	}
	result := response.Result.(map[string]any)
	if result["echoMethod"] != "system.ping" {
		t.Fatalf("unexpected result %+v", result)
	}
}

func TestCallContextCancellationInterruptsPendingAction(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "cc-cancel-")
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
	received := make(chan struct{})
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var request any
		json.NewDecoder(conn).Decode(&request)
		close(received)
		var data [1]byte
		conn.Read(data[:])
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { <-received; cancel() }()
	start := time.Now()
	r, err := CallContext(ctx, path, "tab.click", nil, 20*time.Second)
	if err != nil || r.Error == nil || r.Error.Data.Kind != "OUTCOME_UNKNOWN" {
		t.Fatalf("cancelled action: %+v %v", r, err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("cancellation waited for socket timeout")
	}
	<-serverDone
}

func TestLostResponseToClickIsOutcomeUnknown(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "cc-lost-")
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
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		var request map[string]any
		json.NewDecoder(conn).Decode(&request)
		conn.Close()
	}()
	response, err := Call(path, "tab.click", map[string]any{"profileId": "p", "tabId": 7}, time.Second)
	if err != nil || response.Error == nil || response.Error.Data.Kind != "OUTCOME_UNKNOWN" {
		t.Fatalf("lost response %+v, %v", response, err)
	}
}

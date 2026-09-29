package native

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"chrome-connector/internal/protocol"
)

type shortWriter struct{ bytes.Buffer }

func (writer *shortWriter) Write(payload []byte) (int, error) {
	if len(payload) > 2 {
		payload = payload[:2]
	}
	return writer.Buffer.Write(payload)
}

func TestJSONLineCompletesShortWrites(t *testing.T) {
	var writer shortWriter
	if err := writeJSONLine(&writer, []byte(`{"jsonrpc":"2.0"}`)); err != nil {
		t.Fatal(err)
	}
	if writer.String() != "{\"jsonrpc\":\"2.0\"}\n" {
		t.Fatalf("line %q", writer.String())
	}
}

func TestNativeHostBridgesBrowserAndBroker(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "cc-native-")
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
	browserReader, browserWriter := io.Pipe()
	nativeReader, nativeWriter := io.Pipe()
	defer browserWriter.Close()
	defer nativeReader.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hostDone := make(chan error, 1)
	go func() { hostDone <- Run(ctx, browserReader, nativeWriter, path) }()

	hello, _ := json.Marshal(map[string]any{"kind": "hello", "profileId": "profile-a", "protocolVersion": "1.0", "extensionVersion": "0.1.0", "supportedCdpDomains": []string{"Network"}})
	if err := protocol.WriteNativeFrame(browserWriter, hello, 64*1024*1024); err != nil {
		t.Fatal(err)
	}
	listener.(*net.UnixListener).SetDeadline(time.Now().Add(5 * time.Second))
	brokerConn, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer brokerConn.Close()
	brokerConn.SetDeadline(time.Now().Add(5 * time.Second))
	brokerDecoder := json.NewDecoder(brokerConn)
	brokerEncoder := json.NewEncoder(brokerConn)
	var registered map[string]any
	if err := brokerDecoder.Decode(&registered); err != nil {
		t.Fatal(err)
	}
	if registered["profileId"] != "profile-a" {
		t.Fatalf("registration %+v", registered)
	}
	if domains, ok := registered["supportedCdpDomains"].([]any); !ok || len(domains) != 1 || domains[0] != "Network" {
		t.Fatalf("capability registration %+v", registered)
	}
	if err := brokerEncoder.Encode(map[string]any{"jsonrpc": "2.0", "id": "b-1", "method": "tab.list", "params": map[string]any{"profileId": "profile-a"}}); err != nil {
		t.Fatal(err)
	}
	requestBytes, err := protocol.ReadNativeFrame(nativeReader, 1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	var request map[string]any
	if err := json.Unmarshal(requestBytes, &request); err != nil || request["method"] != "tab.list" {
		t.Fatalf("native request %s, %v", requestBytes, err)
	}
	response, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "b-1", "result": map[string]any{"tabs": []any{}}})
	if err := protocol.WriteNativeFrame(browserWriter, response, 64*1024*1024); err != nil {
		t.Fatal(err)
	}
	var forwarded map[string]any
	if err := brokerDecoder.Decode(&forwarded); err != nil {
		t.Fatal(err)
	}
	if forwarded["id"] != "b-1" {
		t.Fatalf("forwarded response %+v", forwarded)
	}
	cancel()
	browserWriter.Close()
	brokerConn.Close()
	select {
	case <-hostDone:
	case <-time.After(5 * time.Second):
		t.Fatal("native host did not stop")
	}
}

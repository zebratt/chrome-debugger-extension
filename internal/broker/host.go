package broker

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"chrome-connector/internal/protocol"
)

type hostHello struct {
	Kind                string   `json:"kind"`
	ProfileID           string   `json:"profileId"`
	ProtocolVersion     string   `json:"protocolVersion"`
	ExtensionVersion    string   `json:"extensionVersion"`
	SupportedCDPDomains []string `json:"supportedCdpDomains"`
}

type hostSession struct {
	profileID           string
	version             string
	extensionVersion    string
	supportedCDPDomains []string
	conn                net.Conn
	encoder             *json.Encoder
	writeMu             sync.Mutex
	pendingMu           sync.Mutex
	pending             map[string]chan protocol.Response
	done                chan struct{}
	closeOnce           sync.Once
}

func (server *Server) handleHost(conn net.Conn, decoder *json.Decoder, hello hostHello) {
	if hello.ProfileID == "" || !protocol.CompatibleVersion(ProtocolVersion, hello.ProtocolVersion) {
		return
	}
	host := &hostSession{
		profileID:           hello.ProfileID,
		version:             hello.ProtocolVersion,
		extensionVersion:    hello.ExtensionVersion,
		supportedCDPDomains: hello.SupportedCDPDomains,
		conn:                conn,
		encoder:             json.NewEncoder(conn),
		pending:             make(map[string]chan protocol.Response),
		done:                make(chan struct{}),
	}
	server.mutex.Lock()
	previous := server.hosts[hello.ProfileID]
	server.hosts[hello.ProfileID] = host
	server.mutex.Unlock()
	if previous != nil {
		previous.close()
	}
	server.registerOnce.Do(func() { close(server.registered) })
	defer func() {
		host.close()
		server.mutex.Lock()
		if server.hosts[hello.ProfileID] == host {
			delete(server.hosts, hello.ProfileID)
			server.leases.RevokeProfile(hello.ProfileID)
		}
		server.mutex.Unlock()
	}()
	for {
		var response protocol.Response
		if err := decoder.Decode(&response); err != nil {
			return
		}
		key := string(response.ID)
		host.pendingMu.Lock()
		channel := host.pending[key]
		delete(host.pending, key)
		host.pendingMu.Unlock()
		if channel != nil {
			channel <- response
		}
	}
}

func (host *hostSession) close() {
	host.closeOnce.Do(func() {
		close(host.done)
		host.conn.Close()
	})
}

func (server *Server) forward(profileID string, request protocol.Request) protocol.Response {
	var host *hostSession
	deadline := server.started.Add(3 * time.Second)
	for {
		server.mutex.Lock()
		host = server.hosts[profileID]
		server.mutex.Unlock()
		if host != nil || !time.Now().Before(deadline) {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if host == nil {
		return protocol.ErrorResponse(request.ID, "BROWSER_OFFLINE", "Chrome profile is unavailable", true)
	}
	originalID := request.ID
	internalID := fmt.Sprintf("b-%d", atomic.AddUint64(&server.sequence, 1))
	request.ID = json.RawMessage(strconv.Quote(internalID))
	channel := make(chan protocol.Response, 1)
	key := string(request.ID)
	host.pendingMu.Lock()
	host.pending[key] = channel
	host.pendingMu.Unlock()
	defer func() {
		host.pendingMu.Lock()
		delete(host.pending, key)
		host.pendingMu.Unlock()
	}()
	host.writeMu.Lock()
	err := host.encoder.Encode(request)
	host.writeMu.Unlock()
	if err != nil {
		return interruptedResponse(originalID, request.Method, err.Error())
	}
	select {
	case response := <-channel:
		response.ID = originalID
		return response
	case <-host.done:
		return interruptedResponse(originalID, request.Method, "Chrome profile disconnected before confirming the action")
	case <-time.After(15 * time.Second):
		return interruptedResponse(originalID, request.Method, "Chrome did not respond before the deadline")
	}
}

func interruptedResponse(id json.RawMessage, method, detail string) protocol.Response {
	switch method {
	case "tab.open", "tab.navigate", "tab.click", "tab.type", "tab.key", "tab.scroll", "cdp.send":
		return protocol.ErrorResponse(id, "OUTCOME_UNKNOWN", detail+"; re-observe the page before another action", false)
	default:
		return protocol.ErrorResponse(id, "BROWSER_OFFLINE", detail, true)
	}
}

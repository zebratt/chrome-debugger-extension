package broker

import (
	"encoding/json"
	"net"
	"time"

	"chrome-connector/internal/protocol"
	"chrome-connector/internal/version"
)

var publicMethods = []string{
	"system.ping", "system.policy", "system.capabilities", "browser.list",
	"tab.list", "tab.open", "tab.info", "tab.navigate", "tab.snapshot", "tab.screenshot",
	"tab.claim", "tab.renew", "tab.release", "tab.click", "tab.select", "tab.type", "tab.key", "tab.scroll", "tab.wait", "cdp.send",
}

func (server *Server) handleClient(conn net.Conn, decoder *json.Decoder, first json.RawMessage) {
	server.mutex.Lock()
	server.activeClients++
	server.lastActivity = time.Now()
	server.mutex.Unlock()
	defer func() {
		server.mutex.Lock()
		server.activeClients--
		server.lastActivity = time.Now()
		server.mutex.Unlock()
	}()
	encoder := json.NewEncoder(conn)
	current := first
	for {
		request, err := protocol.DecodeRequest(current)
		shutdown := false
		if err != nil {
			encoder.Encode(protocol.ErrorResponse(nil, "INVALID_REQUEST", err.Error(), false))
		} else {
			encoder.Encode(server.dispatch(request))
			shutdown = request.Method == "system.shutdown"
		}
		if shutdown {
			server.stopOnce.Do(func() { close(server.stopRequested) })
			return
		}
		if err := decoder.Decode(&current); err != nil {
			return
		}
	}
}

func (server *Server) dispatch(request protocol.Request) protocol.Response {
	if len(request.Params) > 512*1024 {
		return protocol.ErrorResponse(request.ID, "PAYLOAD_TOO_LARGE", "request params exceed 512 KiB", false)
	}
	switch request.Method {
	case "system.ping":
		return successResponse(request.ID, map[string]any{"protocolVersion": ProtocolVersion, "status": server.discoveryState()})
	case "system.shutdown":
		return successResponse(request.ID, map[string]any{"stopping": true})
	case "extension.reload":
		var params struct {
			ProfileID string `json:"profileId"`
		}
		if err := json.Unmarshal(request.Params, &params); err != nil || params.ProfileID == "" {
			return protocol.ErrorResponse(request.ID, "INVALID_PARAMS", "profileId is required", false)
		}
		return server.forward(params.ProfileID, request)
	case "system.policy":
		return successResponse(request.ID, server.policy)
	case "system.capabilities":
		server.mutex.Lock()
		profiles := make([]map[string]any, 0, len(server.hosts))
		for _, host := range server.hosts {
			effective := make([]string, 0)
			if server.policy.RawCDP {
				for _, domain := range host.supportedCDPDomains {
					for _, allowed := range server.policy.CDPDomains {
						if domain == allowed {
							effective = append(effective, domain)
							break
						}
					}
				}
			}
			profiles = append(profiles, map[string]any{"profileId": host.profileID, "hostVersion": host.hostVersion, "extensionVersion": host.extensionVersion, "browserVersion": host.browserVersion, "supportedCdpDomains": host.supportedCDPDomains, "effectiveCdpDomains": effective})
		}
		server.mutex.Unlock()
		return successResponse(request.ID, map[string]any{"protocolVersion": ProtocolVersion, "brokerVersion": version.Component, "methods": publicMethods, "rawCdpEnabled": server.policy.RawCDP, "allowedCdpDomains": server.policy.CDPDomains, "profiles": profiles})
	case "browser.list":
		server.waitForRegistration()
		server.mutex.Lock()
		profiles := make([]map[string]any, 0, len(server.hosts))
		for _, host := range server.hosts {
			profiles = append(profiles, map[string]any{"profileId": host.profileID, "connectionId": host.connectionID, "status": "online", "protocolVersion": host.version, "hostVersion": host.hostVersion, "extensionVersion": host.extensionVersion, "browserVersion": host.browserVersion})
		}
		for profileID, rejected := range server.rejected {
			if server.hosts[profileID] == nil {
				profiles = append(profiles, map[string]any{"profileId": profileID, "status": "version_mismatch", "protocolVersion": rejected.protocolVersion, "expectedProtocolVersion": ProtocolVersion, "extensionVersion": rejected.extensionVersion})
			}
		}
		server.mutex.Unlock()
		return successResponse(request.ID, map[string]any{"discoveryState": server.discoveryState(), "profiles": profiles})
	case "tab.list":
		var params struct {
			ProfileID string `json:"profileId"`
		}
		if err := json.Unmarshal(request.Params, &params); err != nil || params.ProfileID == "" {
			return protocol.ErrorResponse(request.ID, "INVALID_PARAMS", "profileId is required", false)
		}
		response := server.forward(params.ProfileID, request)
		if response.Error != nil {
			return response
		}
		result, ok := response.Result.(map[string]any)
		if !ok {
			return protocol.ErrorResponse(request.ID, "INVALID_HOST_RESPONSE", "tab.list result is not an object", false)
		}
		tabs, ok := result["tabs"].([]any)
		if !ok {
			return protocol.ErrorResponse(request.ID, "INVALID_HOST_RESPONSE", "tab.list result has no tabs", false)
		}
		filtered := make([]any, 0, len(tabs))
		for _, item := range tabs {
			tab, ok := item.(map[string]any)
			if !ok {
				continue
			}
			url, _ := tab["url"].(string)
			if server.policy.AllowsURL(url, false) {
				filtered = append(filtered, tab)
			}
		}
		result["tabs"] = filtered
		return response
	case "tab.claim", "tab.renew", "tab.release":
		var params targetParams
		if err := json.Unmarshal(request.Params, &params); err != nil || params.ProfileID == "" || params.TabID <= 0 {
			return protocol.ErrorResponse(request.ID, "INVALID_PARAMS", "profileId and tabId are required", false)
		}
		if request.Method == "tab.release" {
			if err := server.leases.Release(params.ProfileID, params.TabID, params.LeaseToken); err != nil {
				return protocol.ErrorResponse(request.ID, "TAB_BUSY", err.Error(), true)
			}
			return successResponse(request.ID, map[string]any{"released": true})
		}
		var lease Lease
		var err error
		if request.Method == "tab.claim" {
			lease, err = server.leases.Claim(params.ProfileID, params.TabID, params.LeaseToken)
		} else {
			lease, err = server.leases.Renew(params.ProfileID, params.TabID, params.LeaseToken)
		}
		if err != nil {
			kind := "TAB_BUSY"
			if err == ErrLeaseExpired {
				kind = "LEASE_EXPIRED"
			}
			return protocol.ErrorResponse(request.ID, kind, err.Error(), true)
		}
		return successResponse(request.ID, map[string]any{"leaseToken": lease.Token, "expiresAt": lease.ExpiresAt})
	case "tab.open":
		var params targetParams
		if err := json.Unmarshal(request.Params, &params); err != nil || params.ProfileID == "" || params.URL == "" {
			return protocol.ErrorResponse(request.ID, "INVALID_PARAMS", "profileId and url are required", false)
		}
		if !server.policy.AllowsURL(params.URL, false) {
			return protocol.ErrorResponse(request.ID, "POLICY_DENIED", "URL is blocked by local policy", false)
		}
		response := server.forward(params.ProfileID, request)
		if response.Error != nil {
			return response
		}
		result, ok := response.Result.(map[string]any)
		if !ok {
			return protocol.ErrorResponse(request.ID, "INVALID_HOST_RESPONSE", "tab.open result is not an object", false)
		}
		tabID, ok := result["tabId"].(float64)
		if !ok || tabID <= 0 {
			return protocol.ErrorResponse(request.ID, "INVALID_HOST_RESPONSE", "tab.open result has no tabId", false)
		}
		lease, err := server.leases.Claim(params.ProfileID, int(tabID), "")
		if err != nil {
			return protocol.ErrorResponse(request.ID, "TAB_BUSY", err.Error(), true)
		}
		result["leaseToken"] = lease.Token
		result["expiresAt"] = lease.ExpiresAt
		return response
	case "tab.navigate", "cdp.send":
		var params targetParams
		if err := json.Unmarshal(request.Params, &params); err != nil || params.ProfileID == "" || params.TabID <= 0 {
			return protocol.ErrorResponse(request.ID, "INVALID_PARAMS", "profileId and tabId are required", false)
		}
		if request.Method == "cdp.send" && !server.policy.AllowsCDP(params.Method) {
			return protocol.ErrorResponse(request.ID, "POLICY_DENIED", "CDP method is blocked by local policy", false)
		}
		if request.Method == "tab.navigate" && !server.policy.AllowsURL(params.URL, false) {
			return protocol.ErrorResponse(request.ID, "POLICY_DENIED", "URL is blocked by local policy", false)
		}
		if err := server.leases.Check(params.ProfileID, params.TabID, params.LeaseToken); err != nil {
			return protocol.ErrorResponse(request.ID, "LEASE_EXPIRED", err.Error(), true)
		}
		url, blocked := server.checkLiveURL(params.ProfileID, params.TabID, request.ID)
		if blocked != nil {
			return *blocked
		}
		return server.forwardAction(params.ProfileID, params.TabID, withExpectedURL(request, url))
	case "tab.info", "tab.snapshot", "tab.screenshot", "tab.wait":
		var params targetParams
		if err := json.Unmarshal(request.Params, &params); err != nil || params.ProfileID == "" || params.TabID <= 0 {
			return protocol.ErrorResponse(request.ID, "INVALID_PARAMS", "profileId and tabId are required", false)
		}
		if request.Method == "tab.info" {
			info := server.forward(params.ProfileID, request)
			if info.Error != nil {
				return info
			}
			result, ok := info.Result.(map[string]any)
			url, hasURL := result["url"].(string)
			if !ok || !hasURL || !server.policy.AllowsURL(url, false) {
				return protocol.ErrorResponse(request.ID, "POLICY_DENIED", "Current tab URL is blocked by local policy", false)
			}
			return info
		}
		url, blocked := server.checkLiveURL(params.ProfileID, params.TabID, request.ID)
		if blocked != nil {
			return *blocked
		}
		return server.forwardAction(params.ProfileID, params.TabID, withExpectedURL(request, url))
	case "tab.click", "tab.select", "tab.type", "tab.key", "tab.scroll":
		var params targetParams
		if err := json.Unmarshal(request.Params, &params); err != nil || params.ProfileID == "" || params.TabID <= 0 {
			return protocol.ErrorResponse(request.ID, "INVALID_PARAMS", "profileId and tabId are required", false)
		}
		if err := server.leases.Check(params.ProfileID, params.TabID, params.LeaseToken); err != nil {
			return protocol.ErrorResponse(request.ID, "LEASE_EXPIRED", err.Error(), true)
		}
		url, blocked := server.checkLiveURL(params.ProfileID, params.TabID, request.ID)
		if blocked != nil {
			return *blocked
		}
		return server.forwardAction(params.ProfileID, params.TabID, withExpectedURL(request, url))
	default:
		return protocol.ErrorResponse(request.ID, "METHOD_NOT_FOUND", "method is not available", false)
	}
}

func (server *Server) checkLiveURL(profileID string, tabID int, id json.RawMessage) (string, *protocol.Response) {
	params, _ := json.Marshal(map[string]any{"profileId": profileID, "tabId": tabID})
	info := server.forward(profileID, protocol.Request{JSONRPC: "2.0", ID: id, Method: "tab.info", Params: params})
	if info.Error != nil {
		return "", &info
	}
	result, ok := info.Result.(map[string]any)
	if !ok {
		response := protocol.ErrorResponse(id, "INVALID_HOST_RESPONSE", "tab.info result is not an object", false)
		return "", &response
	}
	url, ok := result["url"].(string)
	if !ok || !server.policy.AllowsURL(url, false) {
		response := protocol.ErrorResponse(id, "POLICY_DENIED", "Current tab URL is blocked by local policy", false)
		return "", &response
	}
	return url, nil
}

func withExpectedURL(request protocol.Request, url string) protocol.Request {
	var params map[string]any
	_ = json.Unmarshal(request.Params, &params)
	params["expectedUrl"] = url
	request.Params, _ = json.Marshal(params)
	return request
}

func (server *Server) forwardAction(profileID string, tabID int, request protocol.Request) protocol.Response {
	response := server.forward(profileID, request)
	if response.Error != nil && (response.Error.Data.Kind == "TARGET_DETACHED" || response.Error.Data.Kind == "OUTCOME_UNKNOWN") {
		server.leases.RevokeTab(profileID, tabID)
	}
	return response
}

type targetParams struct {
	ProfileID  string `json:"profileId"`
	TabID      int    `json:"tabId"`
	LeaseToken string `json:"leaseToken"`
	URL        string `json:"url"`
	Method     string `json:"method"`
}

func (server *Server) discoveryState() string {
	server.mutex.Lock()
	defer server.mutex.Unlock()
	if len(server.hosts) > 0 {
		return "ready"
	}
	if len(server.rejected) > 0 {
		return "version_mismatch"
	}
	if time.Since(server.started) < 3*time.Second {
		return "discovering"
	}
	return "offline"
}

func (server *Server) waitForRegistration() {
	server.mutex.Lock()
	hasHost := len(server.hosts) > 0 || len(server.rejected) > 0
	server.mutex.Unlock()
	if hasHost {
		return
	}
	timeout := 3*time.Second - time.Since(server.started)
	if timeout <= 0 {
		return
	}
	select {
	case <-server.registered:
	case <-time.After(timeout):
	}
}

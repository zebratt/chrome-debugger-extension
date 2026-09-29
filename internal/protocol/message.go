package protocol

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type ErrorData struct {
	Kind      string `json:"kind"`
	Retryable bool   `json:"retryable"`
}

type RPCError struct {
	Code    int       `json:"code"`
	Message string    `json:"message"`
	Data    ErrorData `json:"data"`
}

type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

func DecodeRequest(payload []byte) (Request, error) {
	var request Request
	if err := json.Unmarshal(payload, &request); err != nil {
		return Request{}, err
	}
	if request.JSONRPC != "2.0" || request.Method == "" || len(request.ID) == 0 || string(request.ID) == "null" {
		return Request{}, errors.New("invalid JSON-RPC request")
	}
	if len(request.Params) > 0 {
		var params map[string]any
		if err := json.Unmarshal(request.Params, &params); err != nil || params == nil {
			return Request{}, errors.New("JSON-RPC params must be an object")
		}
	}
	return request, nil
}

func CompatibleVersion(current, peer string) bool {
	currentMajor, currentMinor, ok := parseVersion(current)
	if !ok {
		return false
	}
	peerMajor, peerMinor, ok := parseVersion(peer)
	return ok && peerMajor == currentMajor && peerMinor <= currentMinor && peerMinor >= currentMinor-1
}

func ErrorResponse(id json.RawMessage, kind, message string, retryable bool) Response {
	return Response{
		JSONRPC: "2.0",
		ID:      id,
		Error: &RPCError{
			Code:    -32000,
			Message: message,
			Data:    ErrorData{Kind: kind, Retryable: retryable},
		},
	}
}

func parseVersion(value string) (int, int, bool) {
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return 0, 0, false
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil || major < 0 {
		return 0, 0, false
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil || minor < 0 {
		return 0, 0, false
	}
	return major, minor, true
}

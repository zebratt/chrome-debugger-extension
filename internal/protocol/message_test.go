package protocol

import (
	"encoding/json"
	"testing"
)

func TestDecodeRequestRejectsInvalidEnvelope(t *testing.T) {
	for _, input := range []string{
		`{"jsonrpc":"1.0","id":1,"method":"system.ping"}`,
		`{"jsonrpc":"2.0","id":1}`,
		`{"jsonrpc":"2.0","method":"system.ping"}`,
		`{"jsonrpc":"2.0","id":1,"method":"system.ping","params":[1]}`,
	} {
		if _, err := DecodeRequest([]byte(input)); err == nil {
			t.Errorf("accepted invalid request %s", input)
		}
	}
}

func TestDecodeRequestPreservesParamsAndID(t *testing.T) {
	request, err := DecodeRequest([]byte(`{"jsonrpc":"2.0","id":"r1","method":"tab.list","params":{"profileId":"p1"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if request.Method != "tab.list" || string(request.ID) != `"r1"` {
		t.Fatalf("unexpected request: %+v", request)
	}
	var params map[string]string
	if err := json.Unmarshal(request.Params, &params); err != nil {
		t.Fatal(err)
	}
	if params["profileId"] != "p1" {
		t.Fatalf("params = %+v", params)
	}
}

func TestVersionCompatibilityAllowsCurrentAndPreviousMinor(t *testing.T) {
	for _, version := range []string{"1.3", "1.2"} {
		if !CompatibleVersion("1.3", version) {
			t.Errorf("version %s should be compatible", version)
		}
	}
	for _, version := range []string{"1.1", "1.4", "2.3", "invalid"} {
		if CompatibleVersion("1.3", version) {
			t.Errorf("version %s should be incompatible", version)
		}
	}
}

func TestErrorResponseHasMachineReadableKind(t *testing.T) {
	response := ErrorResponse(json.RawMessage(`1`), "BROWSER_OFFLINE", "Chrome is unavailable", true)
	bytes, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Error struct {
			Code int `json:"code"`
			Data struct {
				Kind      string `json:"kind"`
				Retryable bool   `json:"retryable"`
			} `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal(bytes, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Error.Code != -32000 || decoded.Error.Data.Kind != "BROWSER_OFFLINE" || !decoded.Error.Data.Retryable {
		t.Fatalf("unexpected response %s", bytes)
	}
}

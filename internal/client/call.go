package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"chrome-connector/internal/protocol"
)

var sequence uint64

func Call(socket, method string, params any, timeout time.Duration) (protocol.Response, error) {
	return CallContext(context.Background(), socket, method, params, timeout)
}

func CallContext(ctx context.Context, socket, method string, params any, timeout time.Duration) (protocol.Response, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	connection, err := (&net.Dialer{}).DialContext(ctx, "unix", socket)
	if err != nil {
		return protocol.Response{}, err
	}
	defer connection.Close()
	stop := context.AfterFunc(ctx, func() { connection.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	if err := connection.SetDeadline(deadline); err != nil {
		return protocol.Response{}, err
	}
	id := fmt.Sprintf("client-%d-%d", os.Getpid(), atomic.AddUint64(&sequence, 1))
	request := protocol.Request{JSONRPC: "2.0", ID: json.RawMessage(strconv.Quote(id)), Method: method}
	if params != nil {
		request.Params, err = json.Marshal(params)
		if err != nil {
			return protocol.Response{}, err
		}
	}
	if err := json.NewEncoder(connection).Encode(request); err != nil {
		return protocol.Response{}, err
	}
	var response protocol.Response
	if err := json.NewDecoder(connection).Decode(&response); err != nil {
		if isAction(method) {
			return protocol.ErrorResponse(request.ID, "OUTCOME_UNKNOWN", "broker response was lost; re-observe the page before another action", false), nil
		}
		return protocol.Response{}, err
	}
	if string(response.ID) != string(request.ID) {
		return protocol.Response{}, fmt.Errorf("response ID %s does not match request %s", response.ID, request.ID)
	}
	return response, nil
}

func isAction(method string) bool {
	switch method {
	case "tab.open", "tab.navigate", "tab.click", "tab.select", "tab.type", "tab.key", "tab.scroll", "cdp.send", "extension.reload":
		return true
	default:
		return false
	}
}

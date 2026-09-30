package mcp

import (
	"context"
	"encoding/json"
	"io"
)

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type incomingMessage struct {
	message rpcMessage
	err     error
}

func readMessages(ctx context.Context, input io.Reader) <-chan incomingMessage {
	ch := make(chan incomingMessage)
	go func() {
		decoder := json.NewDecoder(input)
		for {
			var incoming incomingMessage
			incoming.err = decoder.Decode(&incoming.message)
			select {
			case ch <- incoming:
			case <-ctx.Done():
				return
			}
			if incoming.err != nil {
				return
			}
		}
	}()
	return ch
}

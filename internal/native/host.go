package native

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"chrome-connector/internal/protocol"
)

func writeJSONLine(writer io.Writer, payload []byte) error {
	packet := append(append([]byte{}, payload...), '\n')
	for len(packet) > 0 {
		written, err := writer.Write(packet)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		packet = packet[written:]
	}
	return nil
}

func Run(ctx context.Context, input io.Reader, output io.Writer, socket string) error {
	first, err := protocol.ReadNativeFrame(input, 64*1024*1024)
	if err != nil {
		return err
	}
	var hello struct {
		Kind                string   `json:"kind"`
		ProfileID           string   `json:"profileId"`
		ProtocolVersion     string   `json:"protocolVersion"`
		ExtensionVersion    string   `json:"extensionVersion"`
		SupportedCDPDomains []string `json:"supportedCdpDomains"`
	}
	if err := json.Unmarshal(first, &hello); err != nil || hello.Kind != "hello" || hello.ProfileID == "" {
		return errors.New("invalid extension hello")
	}

	var currentMu sync.RWMutex
	var current net.Conn
	var writeMu sync.Mutex
	nativeDone := make(chan error, 1)
	go func() {
		for {
			message, err := protocol.ReadNativeFrame(input, 64*1024*1024)
			if err != nil {
				nativeDone <- err
				currentMu.RLock()
				if current != nil {
					current.Close()
				}
				currentMu.RUnlock()
				return
			}
			currentMu.RLock()
			connection := current
			currentMu.RUnlock()
			if connection != nil {
				writeMu.Lock()
				if err := writeJSONLine(connection, message); err != nil {
					connection.Close()
				}
				writeMu.Unlock()
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-nativeDone:
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		default:
		}
		connection, err := net.DialTimeout("unix", socket, 200*time.Millisecond)
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(200 * time.Millisecond):
				continue
			}
		}
		currentMu.Lock()
		current = connection
		currentMu.Unlock()
		stop := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				connection.Close()
			case <-stop:
			}
		}()
		writeMu.Lock()
		err = json.NewEncoder(connection).Encode(hello)
		writeMu.Unlock()
		if err == nil {
			decoder := json.NewDecoder(connection)
			for {
				var request json.RawMessage
				if err = decoder.Decode(&request); err != nil {
					break
				}
				if err = protocol.WriteNativeFrame(output, request, 1024*1024); err != nil {
					break
				}
			}
		}
		close(stop)
		currentMu.Lock()
		if current == connection {
			current = nil
		}
		currentMu.Unlock()
		connection.Close()
		if err != nil && errors.Is(err, protocol.ErrMessageTooLarge) {
			return err
		}
	}
}

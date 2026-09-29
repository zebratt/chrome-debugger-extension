package protocol

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

func TestNativeFrameRoundTrip(t *testing.T) {
	payload := []byte(`{"type":"ping","nonce":"test"}`)
	var buffer bytes.Buffer
	if err := WriteNativeFrame(&buffer, payload, 1024); err != nil {
		t.Fatal(err)
	}
	got, err := ReadNativeFrame(&buffer, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("got %q, want %q", got, payload)
	}
}

func TestNativeFrameRejectsOversizeBeforeAllocation(t *testing.T) {
	var buffer bytes.Buffer
	if err := binary.Write(&buffer, binary.NativeEndian, uint32(1025)); err != nil {
		t.Fatal(err)
	}
	_, err := ReadNativeFrame(&buffer, 1024)
	if !errors.Is(err, ErrMessageTooLarge) {
		t.Fatalf("got %v, want ErrMessageTooLarge", err)
	}
}

func TestNativeFrameRejectsTruncatedPayload(t *testing.T) {
	var buffer bytes.Buffer
	if err := binary.Write(&buffer, binary.NativeEndian, uint32(4)); err != nil {
		t.Fatal(err)
	}
	buffer.WriteString("ab")
	_, err := ReadNativeFrame(&buffer, 1024)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("got %v, want io.ErrUnexpectedEOF", err)
	}
}

func TestNativeFrameRejectsOversizeWrite(t *testing.T) {
	var buffer bytes.Buffer
	if err := WriteNativeFrame(&buffer, []byte("oversize"), 4); !errors.Is(err, ErrMessageTooLarge) {
		t.Fatalf("got %v, want ErrMessageTooLarge", err)
	}
	if buffer.Len() != 0 {
		t.Fatalf("oversize write produced %d bytes", buffer.Len())
	}
}

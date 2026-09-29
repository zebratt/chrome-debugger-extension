package protocol

import (
	"encoding/binary"
	"errors"
	"io"
	"math"
)

var ErrMessageTooLarge = errors.New("message too large")

func ReadNativeFrame(reader io.Reader, max uint32) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return nil, err
	}
	size := binary.NativeEndian.Uint32(header[:])
	if size > max {
		return nil, ErrMessageTooLarge
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func WriteNativeFrame(writer io.Writer, payload []byte, max uint32) error {
	if len(payload) > int(max) || len(payload) > math.MaxUint32 {
		return ErrMessageTooLarge
	}
	var header [4]byte
	binary.NativeEndian.PutUint32(header[:], uint32(len(payload)))
	if err := writeAll(writer, header[:]); err != nil {
		return err
	}
	return writeAll(writer, payload)
}

func writeAll(writer io.Writer, bytes []byte) error {
	for len(bytes) > 0 {
		count, err := writer.Write(bytes)
		if err != nil {
			return err
		}
		if count == 0 {
			return io.ErrShortWrite
		}
		bytes = bytes[count:]
	}
	return nil
}

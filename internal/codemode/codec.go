// Package codemode connects Wuu to an isolated JavaScript execution host.
// It does not execute model-generated code in the Go or extension process.
package codemode

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

// The private Node control channel uses little-endian length-prefixed JSON, not JSON lines.
// Check the limit before allocating: a corrupt host must not grow the client
// heap according to an arbitrary length header.
const maxFrameBytes = 64 * 1024 * 1024

func readFrame(r io.Reader, target any) error {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return err
	}
	n := binary.LittleEndian.Uint32(header[:])
	if n == 0 || n > maxFrameBytes {
		return fmt.Errorf("invalid code-mode frame length: %d", n)
	}
	body := make([]byte, int(n))
	if _, err := io.ReadFull(r, body); err != nil {
		return err
	}
	return json.Unmarshal(body, target)
}

func encodeFrame(value any) ([]byte, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(body) > maxFrameBytes {
		return nil, fmt.Errorf("code-mode frame exceeds %d bytes", maxFrameBytes)
	}
	frame := make([]byte, 4+len(body))
	binary.LittleEndian.PutUint32(frame, uint32(len(body)))
	copy(frame[4:], body)
	return frame, nil
}

func writeFrame(w io.Writer, frame []byte) error {
	for len(frame) > 0 {
		n, err := w.Write(frame)
		if err != nil {
			return err
		}
		if n <= 0 || n > len(frame) {
			return io.ErrShortWrite
		}
		frame = frame[n:]
	}
	return nil
}

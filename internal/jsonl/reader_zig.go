package jsonl

// #cgo CFLAGS: -I${SRCDIR}/zig
// #cgo LDFLAGS: ${SRCDIR}/zig/zig-out/lib/libjsonl.a
// #include "jsonl.h"
import "C"
import (
	"bytes"
	"errors"
	"io"
	"strings"
	"unsafe"
)

// readAllFast reads all data from r without the repeated allocation/growth
// overhead of io.ReadAll for known reader types.
func readAllFast(r io.Reader) ([]byte, error) {
	switch v := r.(type) {
	case *bytes.Reader:
		data := make([]byte, v.Len())
		_, err := v.Read(data)
		if err == io.EOF {
			err = nil
		}
		return data, err
	case *strings.Reader:
		data := make([]byte, v.Len())
		_, err := v.Read(data)
		if err == io.EOF {
			err = nil
		}
		return data, err
	default:
		return io.ReadAll(r)
	}
}

// ForEachLineZig reads all data from r, then uses Zig SIMD to find line
// boundaries and calls fn for each line. Behavior is identical to ForEachLine.
func ForEachLineZig(r io.Reader, fn func(line []byte) error) error {
	data, err := readAllFast(r)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}

	// Pre-allocate offsets. For JSONL, average line length is typically
	// 100-1000 bytes. len/10 assumes average line >= 10 bytes, which is
	// very conservative. If the estimate is too small, the retry logic
	// below will allocate the exact size and rescan.
	maxOffsets := len(data)/10 + 100
	if maxOffsets < 64 {
		maxOffsets = 64
	}

	offsets := make([]C.size_t, maxOffsets)
	actual := C.jsonl_scan_lines(
		(*C.uint8_t)(unsafe.Pointer(&data[0])),
		C.size_t(len(data)),
		&offsets[0],
		C.size_t(len(offsets)),
	)

	// If actual > maxOffsets, Zig truncated. Retry with larger buffer.
	// This should be extremely rare for real JSONL.
	if int(actual) > len(offsets) {
		maxOffsets = int(actual) + 1
		offsets = make([]C.size_t, maxOffsets)
		actual = C.jsonl_scan_lines(
			(*C.uint8_t)(unsafe.Pointer(&data[0])),
			C.size_t(len(data)),
			&offsets[0],
			C.size_t(len(offsets)),
		)
	}

	// Use unsafe to avoid bounds checks on every slice operation.
	base := unsafe.Pointer(&data[0])
	dataLen := len(data)

	for i := 0; i < int(actual)-1; i++ {
		start := int(offsets[i])
		end := int(offsets[i+1])
		line := unsafe.Slice((*byte)(unsafe.Add(base, start)), end-start)
		if cbErr := fn(line); cbErr != nil {
			if errors.Is(cbErr, ErrStop) {
				return nil
			}
			return cbErr
		}
	}

	lastStart := int(offsets[actual-1])
	if lastStart < dataLen {
		line := unsafe.Slice((*byte)(unsafe.Add(base, lastStart)), dataLen-lastStart)
		if cbErr := fn(line); cbErr != nil {
			if errors.Is(cbErr, ErrStop) {
				return nil
			}
			return cbErr
		}
	}

	return nil
}

// Package storagecodec provides a small, versioned encoding for large local
// blobs. Encoded values remain self-describing so readers can accept both the
// original plain representation and compressed values during migrations.
package storagecodec

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

var gzipV1Magic = []byte{'W', 'U', 'U', 0, 'G', 'Z', 'I', 'P', 1}

const minCompressionBytes = 4 * 1024

// Encode compresses data when doing so reduces its stored size. Small or
// incompressible values are returned unchanged.
func Encode(data []byte) ([]byte, error) {
	if len(data) < minCompressionBytes {
		return data, nil
	}
	var compressed bytes.Buffer
	compressed.Grow(len(data) / 4)
	compressed.Write(gzipV1Magic)
	writer, err := gzip.NewWriterLevel(&compressed, gzip.BestSpeed)
	if err != nil {
		return nil, fmt.Errorf("create gzip writer: %w", err)
	}
	if _, err := writer.Write(data); err != nil {
		return nil, fmt.Errorf("compress storage blob: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("finish storage blob compression: %w", err)
	}
	if compressed.Len() >= len(data) {
		return data, nil
	}
	return compressed.Bytes(), nil
}

// Decode returns plain values unchanged and expands values written by Encode.
func Decode(data []byte) ([]byte, error) {
	if !bytes.HasPrefix(data, gzipV1Magic) {
		return data, nil
	}
	reader, err := gzip.NewReader(bytes.NewReader(data[len(gzipV1Magic):]))
	if err != nil {
		return nil, fmt.Errorf("open compressed storage blob: %w", err)
	}
	decoded, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil {
		return nil, fmt.Errorf("decompress storage blob: %w", readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("finish storage blob decompression: %w", closeErr)
	}
	return decoded, nil
}

// Encoded reports whether data uses this package's compressed representation.
func Encoded(data []byte) bool {
	return bytes.HasPrefix(data, gzipV1Magic)
}

// CompressFile losslessly rewrites a plain file with the same encoding used by
// Encode. The replacement is atomic, keeps the original permissions, and is
// abandoned if the source changes while compression is running.
func CompressFile(path string) (changed bool, bytesBefore, bytesAfter int64, err error) {
	source, err := os.Open(path)
	if err != nil {
		return false, 0, 0, err
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		return false, 0, 0, err
	}
	bytesBefore = info.Size()
	if bytesBefore < minCompressionBytes {
		return false, bytesBefore, bytesBefore, nil
	}
	prefix := make([]byte, len(gzipV1Magic))
	if _, err := io.ReadFull(source, prefix); err != nil {
		return false, bytesBefore, bytesBefore, nil
	}
	if bytes.Equal(prefix, gzipV1Magic) {
		return false, bytesBefore, bytesBefore, nil
	}
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return false, bytesBefore, bytesBefore, err
	}

	temp, err := os.CreateTemp(filepath.Dir(path), ".wuu-compress-*")
	if err != nil {
		return false, bytesBefore, bytesBefore, err
	}
	tempPath := temp.Name()
	keepTemp := false
	defer func() {
		_ = temp.Close()
		if !keepTemp {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(info.Mode().Perm()); err != nil {
		return false, bytesBefore, bytesBefore, err
	}
	if _, err := temp.Write(gzipV1Magic); err != nil {
		return false, bytesBefore, bytesBefore, err
	}
	writer, err := gzip.NewWriterLevel(temp, gzip.BestSpeed)
	if err != nil {
		return false, bytesBefore, bytesBefore, err
	}
	_, copyErr := io.Copy(writer, source)
	closeWriterErr := writer.Close()
	if copyErr != nil {
		return false, bytesBefore, bytesBefore, copyErr
	}
	if closeWriterErr != nil {
		return false, bytesBefore, bytesBefore, closeWriterErr
	}
	if err := temp.Sync(); err != nil {
		return false, bytesBefore, bytesBefore, err
	}
	tempInfo, err := temp.Stat()
	if err != nil {
		return false, bytesBefore, bytesBefore, err
	}
	bytesAfter = tempInfo.Size()
	if bytesAfter >= bytesBefore {
		return false, bytesBefore, bytesBefore, nil
	}
	current, err := os.Stat(path)
	if err != nil {
		return false, bytesBefore, bytesBefore, err
	}
	if current.Size() != info.Size() || !current.ModTime().Equal(info.ModTime()) {
		return false, bytesBefore, bytesBefore, nil
	}
	if err := temp.Close(); err != nil {
		return false, bytesBefore, bytesBefore, err
	}
	if err := os.Chtimes(tempPath, info.ModTime(), info.ModTime()); err != nil {
		return false, bytesBefore, bytesBefore, err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return false, bytesBefore, bytesBefore, err
	}
	keepTemp = true
	return true, bytesBefore, bytesAfter, nil
}

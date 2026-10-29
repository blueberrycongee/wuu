package securefs

import (
	"crypto/sha256"
	"errors"
	"io"
	"math"
	"os"
)

var errUnexpectedFileSize = errors.New("unexpected file size")

// HashRegularFile hashes a regular file without loading it into memory. It
// rejects symbolic links and files replaced between inspection and open.
func HashRegularFile(path string) ([sha256.Size]byte, int64, error) {
	return hashRegularFile(path, -1)
}

func hashRegularFile(path string, expectedSize int64) ([sha256.Size]byte, int64, error) {
	var result [sha256.Size]byte
	info, err := os.Lstat(path)
	if err != nil {
		return result, 0, err
	}
	if !info.Mode().IsRegular() {
		return result, 0, errors.New("expected a regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return result, 0, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return result, 0, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return result, 0, errors.New("file changed during open")
	}
	if expectedSize >= 0 && opened.Size() != expectedSize {
		return result, 0, errUnexpectedFileSize
	}
	// Never chase a file that grows while it is being hashed. The extra byte
	// detects growth without imposing a maximum size on legitimate files.
	limit := opened.Size()
	if limit < math.MaxInt64 {
		limit++
	}
	var reader io.Reader = io.LimitReader(f, limit)
	h := sha256.New()
	size, err := io.Copy(h, reader)
	if err != nil {
		return result, 0, err
	}
	after, err := f.Stat()
	if err != nil {
		return result, 0, err
	}
	if size != opened.Size() || after.Size() != opened.Size() || !after.ModTime().Equal(opened.ModTime()) {
		return result, 0, errors.New("file changed while hashing")
	}
	copy(result[:], h.Sum(nil))
	return result, size, nil
}

// RegularFileMatches checks length before hashing, so an unexpectedly large
// file is rejected without allocation or a full read.
func RegularFileMatches(path string, expected []byte) (bool, error) {
	actual, size, err := hashRegularFile(path, int64(len(expected)))
	if errors.Is(err, errUnexpectedFileSize) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	want := sha256.Sum256(expected)
	return size == int64(len(expected)) && actual == want, nil
}

package executionenv

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/blueberrycongee/wuu/internal/securefs"
	"github.com/blueberrycongee/wuu/internal/storelock"
)

// The token belongs to the worker session, including reconnecting hosts.
// Serialize first creation across processes; never replace an existing secret.
func loadTransportToken(ctx context.Context, path string) (string, error) {
	lock, err := storelock.AcquireContext(ctx, filepath.Dir(path))
	if err != nil {
		return "", err
	}
	defer lock.Release()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		secret := make([]byte, 32)
		if _, err = rand.Read(secret); err != nil {
			return "", err
		}
		data = []byte(hex.EncodeToString(secret))
		if err = securefs.WriteFileAtomic(path, data); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	}
	decoded, err := hex.DecodeString(string(data))
	if err != nil || len(decoded) != 32 {
		return "", fmt.Errorf("invalid execution worker transport credential")
	}
	return string(data), nil
}

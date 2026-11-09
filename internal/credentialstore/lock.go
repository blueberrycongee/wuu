package credentialstore

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/blueberrycongee/wuu/internal/securefs"
)

// Lock files contain no credentials and must not be removed: replacing their
// inode could admit a second writer while the first still owns the old lock.
func acquireStoreLock(ctx context.Context, path string) (*os.File, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	file, err := securefs.OpenFile(path, os.O_CREATE|os.O_RDWR, securefs.FileMode)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, fmt.Errorf("credentialstore: invalid lock file")
	}
	for {
		if err := ctx.Err(); err != nil {
			_ = file.Close()
			return nil, err
		}
		locked, err := tryStoreLock(file)
		if err != nil {
			_ = file.Close()
			return nil, fmt.Errorf("credentialstore: acquire lock: %w", err)
		}
		if locked {
			return file, nil
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			_ = file.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

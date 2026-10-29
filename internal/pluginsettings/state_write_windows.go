//go:build windows

package pluginsettings

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/blueberrycongee/wuu/internal/securefs"
	"golang.org/x/sys/windows"
)

func writeStateFile(path string, data []byte) error {
	if err := securefs.Mkdir(filepath.Dir(path)); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".plugin-state-*.tmp")
	if err != nil {
		return fmt.Errorf("create plugin storage temporary file: %w", err)
	}
	defer os.Remove(temp.Name()) //nolint:errcheck -- removed by the successful rename
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write plugin storage temporary file: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("sync plugin storage temporary file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close plugin storage temporary file: %w", err)
	}
	from, err := windows.UTF16PtrFromString(temp.Name())
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	// Windows does not support fsync on an ordinary directory handle. Request
	// write-through on the atomic replacement instead of weakening fence order.
	if err := windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH); err != nil {
		return fmt.Errorf("replace plugin storage: %w", err)
	}
	return nil
}

// FlushFileBuffers requires a write-capable handle, but does not rewrite the
// file. It confirms existing data and metadata without replacing a live receipt.
func syncStateFile(path string) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open plugin storage for sync: %w", err)
	}
	defer file.Close() //nolint:errcheck -- the sync result is authoritative
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync plugin storage: %w", err)
	}
	return nil
}

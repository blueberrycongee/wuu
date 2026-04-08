package tui

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func pasteImageFromClipboard() (providers.InputImage, error) {
	tmp, err := os.CreateTemp("", "wuu-clipboard-*")
	if err != nil {
		return providers.InputImage{}, fmt.Errorf("create temp file: %w", err)
	}
	path := tmp.Name()
	if closeErr := tmp.Close(); closeErr != nil {
		return providers.InputImage{}, fmt.Errorf("close temp file: %w", closeErr)
	}
	defer os.Remove(path)

	mediaType, err := writeClipboardImage(path)
	if err != nil {
		return providers.InputImage{}, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return providers.InputImage{}, fmt.Errorf("read clipboard image: %w", err)
	}
	if len(data) == 0 {
		return providers.InputImage{}, errors.New("clipboard image was empty")
	}

	if detected := detectImageMediaType(data); detected != "" {
		mediaType = detected
	}
	if mediaType == "" {
		return providers.InputImage{}, errors.New("unsupported clipboard image format")
	}

	return providers.InputImage{
		MediaType: mediaType,
		Data:      base64.StdEncoding.EncodeToString(data),
	}, nil
}

func writeClipboardImage(path string) (string, error) {
	switch runtime.GOOS {
	case "darwin":
		return writeClipboardImageDarwin(path)
	case "linux":
		return writeClipboardImageLinux(path)
	case "windows":
		return writeClipboardImageWindows(path)
	default:
		return "", fmt.Errorf("image paste unsupported on %s", runtime.GOOS)
	}
}

func writeClipboardImageDarwin(path string) (string, error) {
	scriptPath := escapeAppleScriptString(path)
	args := []string{
		"-e", "set png_data to (the clipboard as «class PNGf»)",
		"-e", fmt.Sprintf(`set fp to open for access POSIX file "%s" with write permission`, scriptPath),
		"-e", "write png_data to fp",
		"-e", "close access fp",
	}
	out, err := exec.Command("osascript", args...).CombinedOutput()
	if err != nil {
		msg := cleanCommandOutput(out)
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("no image in clipboard (macOS): %s", msg)
	}
	return "image/png", nil
}

func writeClipboardImageLinux(path string) (string, error) {
	quotedPath := shellQuote(path)
	attempts := []struct {
		mediaType string
		command   string
	}{
		{mediaType: "image/png", command: fmt.Sprintf(`xclip -selection clipboard -t image/png -o > %s 2>/dev/null`, quotedPath)},
		{mediaType: "image/png", command: fmt.Sprintf(`wl-paste --type image/png > %s 2>/dev/null`, quotedPath)},
		{mediaType: "image/jpeg", command: fmt.Sprintf(`xclip -selection clipboard -t image/jpeg -o > %s 2>/dev/null`, quotedPath)},
		{mediaType: "image/jpeg", command: fmt.Sprintf(`wl-paste --type image/jpeg > %s 2>/dev/null`, quotedPath)},
		{mediaType: "image/webp", command: fmt.Sprintf(`xclip -selection clipboard -t image/webp -o > %s 2>/dev/null`, quotedPath)},
		{mediaType: "image/webp", command: fmt.Sprintf(`wl-paste --type image/webp > %s 2>/dev/null`, quotedPath)},
	}

	var lastErr error
	for _, attempt := range attempts {
		_ = os.Remove(path)
		out, err := exec.Command("sh", "-c", attempt.command).CombinedOutput()
		if err != nil {
			msg := cleanCommandOutput(out)
			if msg == "" {
				msg = err.Error()
			}
			lastErr = errors.New(msg)
			continue
		}
		info, statErr := os.Stat(path)
		if statErr == nil && info.Size() > 0 {
			return attempt.mediaType, nil
		}
	}

	if lastErr != nil {
		return "", fmt.Errorf("no image in clipboard (linux): %v", lastErr)
	}
	return "", errors.New("no image in clipboard (linux): install xclip or wl-paste")
}

func writeClipboardImageWindows(path string) (string, error) {
	escapedPath := strings.ReplaceAll(path, "'", "''")
	script := fmt.Sprintf(
		`$img = Get-Clipboard -Format Image; if ($img -eq $null) { exit 1 }; $img.Save('%s', [System.Drawing.Imaging.ImageFormat]::Png)`,
		escapedPath,
	)

	var lastErr error
	for _, binary := range []string{"powershell", "powershell.exe"} {
		_ = os.Remove(path)
		out, err := exec.Command(binary, "-NoProfile", "-Command", script).CombinedOutput()
		if err != nil {
			msg := cleanCommandOutput(out)
			if msg == "" {
				msg = err.Error()
			}
			lastErr = errors.New(msg)
			continue
		}
		info, statErr := os.Stat(path)
		if statErr == nil && info.Size() > 0 {
			return "image/png", nil
		}
	}

	if lastErr != nil {
		return "", fmt.Errorf("no image in clipboard (windows): %v", lastErr)
	}
	return "", errors.New("no image in clipboard (windows)")
}

func detectImageMediaType(data []byte) string {
	if len(data) >= 8 && bytes.Equal(data[:8], []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}) {
		return "image/png"
	}
	if len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
		return "image/jpeg"
	}
	if len(data) >= 6 && (bytes.Equal(data[:6], []byte("GIF87a")) || bytes.Equal(data[:6], []byte("GIF89a"))) {
		return "image/gif"
	}
	if len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")) {
		return "image/webp"
	}
	if len(data) >= 2 && bytes.Equal(data[:2], []byte("BM")) {
		return "image/bmp"
	}
	return ""
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func escapeAppleScriptString(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return replacer.Replace(value)
}

func cleanCommandOutput(out []byte) string {
	msg := strings.TrimSpace(string(out))
	if len(msg) > 180 {
		return msg[:180] + "..."
	}
	return msg
}

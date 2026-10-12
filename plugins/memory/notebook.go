package memory

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	indexFileName     = "MEMORY.md"
	maxIndexLines     = 200
	maxIndexBytes     = 25_000
	removedLineNotice = "[memory line removed: security]"
)

var indexThreatPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)ignore\s+(previous|all|above|prior)\s+instructions`),
	regexp.MustCompile(`(?i)you\s+are\s+now\s+`),
	regexp.MustCompile(`(?i)do\s+not\s+tell\s+the\s+user`),
	regexp.MustCompile(`(?i)system\s+prompt\s+override`),
	regexp.MustCompile(`(?i)disregard\s+(your|all|any)\s+(instructions|rules|guidelines)`),
	regexp.MustCompile(`(?i)act\s+as\s+(if|though)\s+you\s+(have\s+no|don'?t\s+have)\s+(restrictions|limits|rules)`),
	regexp.MustCompile(`(?i)(curl|wget)\s+[^\n]*\$\{?\w*(KEY|TOKEN|SECRET|PASSWORD|CREDENTIAL|API)`),
	regexp.MustCompile(`(?i)cat\s+[^\n]*(\.env|credentials|\.netrc|\.pgpass|\.npmrc|\.pypirc)`),
	regexp.MustCompile(`(?i)authorized_keys|\$HOME/\.ssh|~/\.ssh|\$HOME/\.wuu/\.env|~/\.wuu/\.env`),
}

var indexInvisibleChars = []rune{
	'\u200b', '\u200c', '\u200d', '\u2060', '\ufeff',
	'\u202a', '\u202b', '\u202c', '\u202d', '\u202e',
}

func userNotebook(wuuHome string) string {
	home := strings.TrimSpace(wuuHome)
	if home == "" {
		return ""
	}
	return filepath.Join(home, "memory")
}

func ensureNotebook(dir string) error {
	if strings.TrimSpace(dir) == "" {
		return errorsNewNotebookPath()
	}
	return os.MkdirAll(dir, 0o755)
}

func errorsNewNotebookPath() error {
	return fmt.Errorf("memory: notebook path is required")
}

// readNotebookFile bounds source reads and keeps both service and tool reads
// inside the notebook even when a filename has been replaced by a symlink.
func readNotebookFile(dir, name string) ([]byte, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("memory file must be a regular file")
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxMemoryFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxMemoryFileBytes {
		return nil, fmt.Errorf("memory file exceeds %d bytes", maxMemoryFileBytes)
	}
	if !utf8.Valid(raw) {
		return nil, fmt.Errorf("memory file must contain UTF-8 text")
	}
	return raw, nil
}

func safeIndex(content string, maxBytes int) (string, bool) {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if unsafeIndexLine(line) {
			lines[i] = removedLineNotice
		}
	}
	trimmed := strings.TrimSpace(strings.Join(lines, "\n"))
	allLines := strings.Split(trimmed, "\n")
	lineTruncated := len(allLines) > maxIndexLines
	if lineTruncated {
		trimmed = strings.Join(allLines[:maxIndexLines], "\n")
	}
	if len(trimmed) > maxBytes {
		if cut := strings.LastIndexByte(trimmed[:maxBytes], '\n'); cut > 0 {
			return trimmed[:cut], true
		}
	}
	trimmed, byteTruncated := boundedNotebookText(trimmed, maxBytes)
	return trimmed, lineTruncated || byteTruncated
}

func boundedNotebookText(content string, maxBytes int) (string, bool) {
	if len(content) <= maxBytes {
		return content, false
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(content[cut]) {
		cut--
	}
	return content[:cut], true
}

func unsafeIndexLine(line string) bool {
	for _, char := range indexInvisibleChars {
		if strings.ContainsRune(line, char) {
			return true
		}
	}
	for _, pattern := range indexThreatPatterns {
		if pattern.MatchString(line) {
			return true
		}
	}
	return false
}

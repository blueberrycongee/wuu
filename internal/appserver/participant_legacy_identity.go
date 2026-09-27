package appserver

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
)

const (
	participantAvatarFileName              = "avatar"
	participantAvatarMimeName              = "avatar.mime"
	participantSummaryAvatarMaxBytes int64 = 64 * 1024
)

func participantAvatarImagePath(workspace string) string {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return ""
	}
	return filepath.Join(workspace, participantAvatarFileName)
}

func participantAvatarImageMimePath(workspace string) string {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return ""
	}
	return filepath.Join(workspace, participantAvatarMimeName)
}

func participantSummaryAvatarDataURL(workspace string) string {
	avatar, err := readParticipantAvatarDataURLCapped(workspace, participantSummaryAvatarMaxBytes)
	if err != nil {
		providers.DebugLogf("read summary avatar from %q: %v", workspace, err)
		return ""
	}
	return avatar
}

func readParticipantAvatarDataURLCapped(workspace string, maxBytes int64) (string, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return "", nil
	}
	imgPath := participantAvatarImagePath(workspace)
	info, err := os.Stat(imgPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("stat avatar image: %w", err)
	}
	if info.Size() > maxBytes {
		return "", nil
	}
	mime := "image/png"
	if raw, err := os.ReadFile(participantAvatarImageMimePath(workspace)); err == nil {
		if trimmed := strings.TrimSpace(strings.ToLower(string(raw))); trimmed != "" {
			mime = trimmed
		}
	}
	data, err := os.ReadFile(imgPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("read avatar image: %w", err)
	}
	return fmt.Sprintf("data:%s;base64,%s", mime, base64.StdEncoding.EncodeToString(data)), nil
}

func (s *Server) invalidateParticipantSummary(id string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	s.participantMu.Lock()
	delete(s.participantSummaryCache, id)
	s.participantMu.Unlock()
}

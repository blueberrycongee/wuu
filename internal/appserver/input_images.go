package appserver

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

const inputImageRetention = 7 * 24 * time.Hour

// Only submission creates working copies: loading history must not recreate
// files the user or model has moved or deleted. Vision history stays separate.
func (s *Server) userMessageWithInputImages(threadID, prompt string, images []providers.InputImage, files []providers.InputFile, received []TurnStartImage, parts ...[]providers.MessageContentPart) (msg providers.ChatMessage, err error) {
	msg, err = userMessageFromPrompt(prompt, images, files, parts...)
	if err != nil || len(received) == 0 {
		return msg, err
	}
	if threadID == "." || !filepath.IsLocal(threadID) || filepath.Base(threadID) != threadID {
		return msg, errors.New("invalid image thread id")
	}
	// Decode the complete batch before writing, retaining the received bytes
	// rather than the resized imageproc result sent to the vision provider.
	payloads := make([][]byte, len(received))
	extensions := make([]string, len(received))
	for index, image := range received {
		_, data, decodeErr := normalizeImagePayload(strings.TrimSpace(image.MediaType), strings.TrimSpace(image.Data))
		if decodeErr != nil {
			return msg, decodeErr
		}
		payloads[index], err = base64.StdEncoding.DecodeString(data)
		if err != nil {
			return msg, fmt.Errorf("decode input image %d: %w", index+1, err)
		}
		switch http.DetectContentType(payloads[index]) {
		case "image/png":
			extensions[index] = ".png"
		case "image/jpeg":
			extensions[index] = ".jpg"
		case "image/gif":
			extensions[index] = ".gif"
		case "image/webp":
			extensions[index] = ".webp"
		default:
			return msg, fmt.Errorf("unsupported input image %d", index+1)
		}
	}
	stateDir, err := s.workspaceStateDir()
	if err != nil {
		return msg, err
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return msg, err
	}
	root, err := os.OpenRoot(stateDir)
	if err != nil {
		return msg, err
	}
	defer root.Close()
	dir := filepath.Join("sessions", threadID, "input-images")
	if err := root.MkdirAll(dir, 0o700); err != nil {
		return msg, err
	}
	info, err := root.Lstat(dir)
	if err != nil {
		return msg, err
	}
	if !info.IsDir() {
		return msg, errors.New("input image cache must be a real directory")
	}
	var created []string
	defer func() {
		if err != nil {
			for _, path := range created {
				_ = root.Remove(path)
			}
		}
	}()
	var references strings.Builder
	fmt.Fprintf(&references, "\n\nAttached image files (working copies expire after %g days; copy or move into the workspace to keep):", inputImageRetention.Hours()/24)
	for index, data := range payloads {
		name := fmt.Sprintf("image-%d-%s%s", time.Now().Unix(), rand.Text(), extensions[index])
		path := filepath.Join(dir, name)
		file, openErr := root.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if openErr != nil {
			return msg, openErr
		}
		created = append(created, path)
		_, writeErr := file.Write(data)
		if err := errors.Join(writeErr, file.Close()); err != nil {
			return msg, err
		}
		msg.Images[index].LocalPath = filepath.Join(stateDir, path)
		fmt.Fprintf(&references, "\nImage %d: %s", index+1, msg.Images[index].LocalPath)
	}
	if msg.DisplayContent == "" {
		msg.DisplayContent = msg.Content
	}
	msg.Content += references.String()
	return msg, nil
}

// Creation time is encoded in the generated name so edits and fork copies do
// not reset retention. Unrecognized files are never owned by this cleanup.
func inputImageCreatedAt(name string) (time.Time, bool) {
	ext := filepath.Ext(name)
	if ext != ".png" && ext != ".jpg" && ext != ".gif" && ext != ".webp" {
		return time.Time{}, false
	}
	parts := strings.Split(strings.TrimSuffix(name, ext), "-")
	if len(parts) != 3 || parts[0] != "image" || len(parts[2]) != 26 {
		return time.Time{}, false
	}
	for _, c := range parts[2] {
		if !strings.ContainsRune("ABCDEFGHIJKLMNOPQRSTUVWXYZ234567", c) {
			return time.Time{}, false
		}
	}
	seconds, err := strconv.ParseInt(parts[1], 10, 64)
	return time.Unix(seconds, 0), err == nil
}

func readInputImageDirectory(root *os.Root, path string) ([]os.DirEntry, error) {
	dir, err := root.Open(path)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	return dir.ReadDir(-1)
}

func maintainInputImageStorage(stateDir string, now time.Time) error {
	root, err := os.OpenRoot(stateDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	sessions, err := readInputImageDirectory(root, "sessions")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var failures []error
	for _, session := range sessions {
		if !session.IsDir() {
			continue
		}
		dir := filepath.Join("sessions", session.Name(), "input-images")
		info, err := root.Lstat(dir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if !info.IsDir() {
			continue
		}
		entries, err := readInputImageDirectory(root, dir)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		for _, entry := range entries {
			created, owned := inputImageCreatedAt(entry.Name())
			if !owned || entry.IsDir() || now.Before(created.Add(inputImageRetention)) {
				continue
			}
			// Remove the directory entry, never a symlink's target or a tree.
			if err := root.Remove(filepath.Join(dir, entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
				failures = append(failures, err)
			}
		}
	}
	return errors.Join(failures...)
}

func preserveForkInputImages(stateDir, sourceThreadID, forkThreadID string, history []providers.ChatMessage) error {
	sourceDir := filepath.Join(statepath.SessionArtifactDir(stateDir, sourceThreadID), "input-images")
	forkDir := filepath.Join("sessions", forkThreadID, "input-images")
	var root *os.Root
	defer func() {
		if root != nil {
			_ = root.Close()
		}
	}()
	copied := make(map[string]bool)
	for index := range history {
		msg := &history[index]
		msg.Images = slices.Clone(msg.Images)
		for imageIndex := range msg.Images {
			image := &msg.Images[imageIndex]
			oldPath := image.LocalPath
			if oldPath == "" || filepath.Dir(oldPath) != sourceDir {
				continue
			}
			name := filepath.Base(oldPath)
			if _, owned := inputImageCreatedAt(name); !owned {
				continue
			}
			if root == nil {
				var err error
				root, err = os.OpenRoot(stateDir)
				if err != nil {
					return err
				}
			}
			target := filepath.Join(forkDir, name)
			if !copied[oldPath] {
				source := filepath.Join("sessions", sourceThreadID, "input-images", name)
				info, err := root.Lstat(source)
				if err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
				// Missing/moved/expired copies stay missing; the independent
				// base64 vision history is not a source for file regeneration.
				if err == nil && info.Mode().IsRegular() {
					data, err := root.ReadFile(source)
					if err != nil && !errors.Is(err, os.ErrNotExist) {
						return err
					}
					if err == nil {
						if err := root.MkdirAll(forkDir, 0o700); err != nil {
							return err
						}
						file, err := root.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
						if err != nil {
							return err
						}
						_, writeErr := file.Write(data)
						if err := errors.Join(writeErr, file.Close()); err != nil {
							return err
						}
					}
				}
				copied[oldPath] = true
			}
			image.LocalPath = filepath.Join(stateDir, target)
			msg.Content = strings.ReplaceAll(msg.Content, oldPath, image.LocalPath)
		}
	}
	return nil
}

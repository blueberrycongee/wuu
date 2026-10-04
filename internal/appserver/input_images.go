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

const inputAttachmentRetention = 7 * 24 * time.Hour

// Only submission creates working copies: loading history must not recreate
// files the user or model has moved or deleted. Vision history stays separate.
func (s *Server) userMessageWithInputImages(threadID, prompt string, images []providers.InputImage, files []providers.InputFile, received []TurnStartImage, parts ...[]providers.MessageContentPart) (msg providers.ChatMessage, err error) {
	msg, err = userMessageFromPrompt(prompt, images, files, parts...)
	if err != nil {
		return msg, err
	}
	content, selected, err := s.renderExplicitSkillPrompt(threadID, prompt)
	if err != nil {
		return msg, err
	}
	if selected {
		msg.Content = content
		msg.DisplayContent = strings.TrimSpace(prompt)
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
	var textPartIndexes []int
	for index, part := range msg.ContentParts {
		if part.Type == "pasted_text" {
			textPartIndexes = append(textPartIndexes, index)
			payloads = append(payloads, []byte(part.Text))
			extensions = append(extensions, ".txt")
		}
	}
	if len(payloads) == 0 {
		return msg, nil
	}
	if threadID == "." || !filepath.IsLocal(threadID) || filepath.Base(threadID) != threadID {
		return msg, errors.New("invalid attachment thread id")
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
	var created []string
	defer func() {
		if err != nil {
			for _, path := range created {
				_ = root.Remove(path)
			}
		}
	}()
	for index, data := range payloads {
		directory, prefix := "input-images", "image"
		if index >= len(received) {
			directory, prefix = "input-attachments", "text"
		}
		dir := filepath.Join("sessions", threadID, directory)
		if err := root.MkdirAll(dir, 0o700); err != nil {
			return msg, err
		}
		info, err := root.Lstat(dir)
		if err != nil {
			return msg, err
		}
		if !info.IsDir() {
			return msg, errors.New("input attachment cache must be a real directory")
		}
		name := fmt.Sprintf("%s-%d-%s%s", prefix, time.Now().Unix(), rand.Text(), extensions[index])
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
		if index < len(received) {
			msg.Images[index].LocalPath = filepath.Join(stateDir, path)
		} else {
			msg.ContentParts[textPartIndexes[index-len(received)]].LocalPath = filepath.Join(stateDir, path)
		}
	}
	if msg.DisplayContent == "" {
		msg.DisplayContent = msg.Content
	}
	if len(textPartIndexes) > 0 {
		var content strings.Builder
		for _, part := range msg.ContentParts {
			if part.Type == "pasted_text" {
				content.WriteString(inputTextAttachmentReference(part))
			} else {
				content.WriteString(part.Text)
			}
		}
		modelPrompt := strings.TrimSpace(content.String())
		msg.Content, _, _ = renderLightweightSlashCommandPrompt(modelPrompt)
		if selected {
			// Validate skills before writing, then expand using file references
			// rather than embedding the pasted snapshots in the skill prompt.
			msg.Content, _, err = s.renderExplicitSkillPrompt(threadID, modelPrompt)
			if err != nil {
				return msg, err
			}
		}
	}
	msg.Content += inputImagePathReference(msg.Images)
	return msg, nil
}

func inputTextAttachmentReference(part providers.MessageContentPart) string {
	name := part.Title
	if name == "" {
		name = filepath.Base(part.LocalPath)
	}
	return fmt.Sprintf("\n\nAttached text file %q (working copy expires after %g days; read with file tools as needed):\n%s\n\n", name, inputAttachmentRetention.Hours()/24, part.LocalPath)
}

// Use the same suffix for model input and public-input projection so retries
// do not resubmit server-added paths as authored text.
func inputImagePathReference(images []providers.InputImage) string {
	var references strings.Builder
	for index, image := range images {
		if image.LocalPath == "" {
			continue
		}
		if references.Len() == 0 {
			fmt.Fprintf(&references, "\n\nAttached image files (working copies expire after %g days; copy or move into the workspace to keep):", inputAttachmentRetention.Hours()/24)
		}
		fmt.Fprintf(&references, "\nImage %d: %s", index+1, image.LocalPath)
	}
	return references.String()
}

// Creation time is encoded in the generated name so edits and fork copies do
// not reset retention. Unrecognized files are never owned by this cleanup.
func inputAttachmentCreatedAt(name string) (time.Time, bool) {
	ext := filepath.Ext(name)
	parts := strings.Split(strings.TrimSuffix(name, ext), "-")
	if len(parts) != 3 || len(parts[2]) != 26 {
		return time.Time{}, false
	}
	if !((parts[0] == "text" && ext == ".txt") || (parts[0] == "image" && (ext == ".png" || ext == ".jpg" || ext == ".gif" || ext == ".webp"))) {
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
		for _, directory := range []string{"input-images", "input-attachments"} {
			dir := filepath.Join("sessions", session.Name(), directory)
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
				created, owned := inputAttachmentCreatedAt(entry.Name())
				if !owned || entry.IsDir() || now.Before(created.Add(inputAttachmentRetention)) {
					continue
				}
				// Remove the directory entry, never a symlink's target or a tree.
				if err := root.Remove(filepath.Join(dir, entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
					failures = append(failures, err)
				}
			}
		}
	}
	return errors.Join(failures...)
}

func preserveForkInputImages(stateDir, sourceThreadID, forkThreadID string, history []providers.ChatMessage) error {
	sourceRoot := statepath.SessionArtifactDir(stateDir, sourceThreadID)
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
		msg.ContentParts = providers.CloneMessageContentParts(msg.ContentParts)
		var paths []*string
		for imageIndex := range msg.Images {
			paths = append(paths, &msg.Images[imageIndex].LocalPath)
		}
		for partIndex := range msg.ContentParts {
			if msg.ContentParts[partIndex].Type == "pasted_text" {
				paths = append(paths, &msg.ContentParts[partIndex].LocalPath)
			}
		}
		for _, path := range paths {
			oldPath := *path
			directory := filepath.Base(filepath.Dir(oldPath))
			if oldPath == "" || (directory != "input-images" && directory != "input-attachments") || filepath.Dir(oldPath) != filepath.Join(sourceRoot, directory) {
				continue
			}
			forkDir := filepath.Join("sessions", forkThreadID, directory)
			name := filepath.Base(oldPath)
			if _, owned := inputAttachmentCreatedAt(name); !owned {
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
				source := filepath.Join("sessions", sourceThreadID, directory, name)
				info, err := root.Lstat(source)
				if err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
				// Missing/moved/expired copies stay missing; the independent
				// history snapshot is not a source for file regeneration.
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
			*path = filepath.Join(stateDir, target)
			msg.Content = strings.ReplaceAll(msg.Content, oldPath, *path)
		}
	}
	return nil
}

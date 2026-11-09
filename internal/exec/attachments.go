package exec

import (
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/blueberrycongee/wuu/internal/appserver"
	"github.com/blueberrycongee/wuu/internal/imageproc"
)

func resolveRunAttachments(opts Options) (Attachments, error) {
	rootDir, err := resolveWorkdir(opts.Workdir)
	if err != nil {
		return Attachments{}, err
	}
	attachments := Attachments{
		Images: append([]appserver.TurnStartImage(nil), opts.Attachments.Images...),
		Files:  append([]appserver.TurnStartFile(nil), opts.Attachments.Files...),
	}
	for _, path := range opts.ImagePaths {
		image, err := loadTurnImage(rootDir, path, opts.ImageOriginal)
		if err != nil {
			return Attachments{}, err
		}
		attachments.Images = append(attachments.Images, image)
	}
	for _, path := range opts.FilePaths {
		file, err := loadTurnFile(rootDir, path)
		if err != nil {
			return Attachments{}, err
		}
		attachments.Files = append(attachments.Files, file)
	}
	return attachments, nil
}

func loadTurnImage(rootDir, inputPath string, original bool) (appserver.TurnStartImage, error) {
	data, absPath, err := readAttachmentFile(rootDir, inputPath)
	if err != nil {
		return appserver.TurnStartImage{}, err
	}
	mediaType := detectMediaType(absPath, data)
	if !strings.HasPrefix(mediaType, "image/") {
		return appserver.TurnStartImage{}, fmt.Errorf("--image %s has media type %s, expected image/*", inputPath, mediaType)
	}
	// Validate the local file before creating a thread, but leave image
	// normalization to app-server so every shell follows one code path.
	result, err := imageproc.Encode(absPath, data, imageproc.Options{Mode: imageproc.ModeOriginal})
	if err != nil {
		var ipErr *imageproc.Error
		if errors.As(err, &ipErr) {
			return appserver.TurnStartImage{}, fmt.Errorf("--image %s: %w", inputPath, ipErr)
		}
		return appserver.TurnStartImage{}, fmt.Errorf("--image %s: %w", inputPath, err)
	}
	return appserver.TurnStartImage{
		MediaType: result.MediaType,
		Data:      base64.StdEncoding.EncodeToString(result.Bytes),
		Original:  original,
	}, nil
}

func loadTurnFile(rootDir, inputPath string) (appserver.TurnStartFile, error) {
	data, absPath, err := readAttachmentFile(rootDir, inputPath)
	if err != nil {
		return appserver.TurnStartFile{}, err
	}
	mediaType := detectMediaType(absPath, data)
	if mediaType != "application/pdf" {
		return appserver.TurnStartFile{}, fmt.Errorf("--file %s has media type %s; only PDF files are supported", inputPath, mediaType)
	}
	return appserver.TurnStartFile{
		MediaType: mediaType,
		Data:      base64.StdEncoding.EncodeToString(data),
		Filename:  filepath.Base(absPath),
	}, nil
}

func readAttachmentFile(rootDir, inputPath string) ([]byte, string, error) {
	path := strings.TrimSpace(inputPath)
	if path == "" {
		return nil, "", fmt.Errorf("attachment path is required")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(rootDir, path)
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, "", fmt.Errorf("resolve attachment path %q: %w", inputPath, err)
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return nil, "", fmt.Errorf("stat attachment %q: %w", inputPath, err)
	}
	if info.IsDir() {
		return nil, "", fmt.Errorf("attachment %q is a directory", inputPath)
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, "", fmt.Errorf("read attachment %q: %w", inputPath, err)
	}
	if len(data) == 0 {
		return nil, "", fmt.Errorf("attachment %q is empty", inputPath)
	}
	return data, absPath, nil
}

func detectMediaType(absPath string, data []byte) string {
	if extType := mime.TypeByExtension(strings.ToLower(filepath.Ext(absPath))); extType != "" {
		if mediaType, _, err := mime.ParseMediaType(extType); err == nil && mediaType != "" {
			return mediaType
		}
	}
	return http.DetectContentType(data)
}

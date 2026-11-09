package authstorage

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/credentialstore"
)

type nativeClaudeSource struct {
	home, directory, account string
	defaultDirectory         bool
}

func newNativeClaudeSource(home string) nativeClaudeSource {
	directory := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR"))
	source := nativeClaudeSource{home: home, directory: directory, defaultDirectory: directory == "", account: os.Getenv("USER")}
	if directory == "" {
		source.directory = filepath.Join(home, ".claude")
	}
	if source.account == "" {
		if current, err := user.Current(); err == nil {
			source.account = current.Username
		}
	}
	if !claudeKeychainAccountPattern.MatchString(source.account) {
		source.account = "claude-code-user"
	}
	return source
}

func (s nativeClaudeSource) read(ctx context.Context) (Credentials, error) {
	file, err := os.Open(filepath.Join(s.directory, ".credentials.json"))
	if err == nil {
		defer file.Close()
		body, err := io.ReadAll(io.LimitReader(file, 1<<20+1))
		defer clear(body)
		if err != nil || len(body) > 1<<20 {
			return Credentials{}, errors.New("Claude CLI credential file is unavailable")
		}
		return decodeClaudeCredential(body)
	}
	if !os.IsNotExist(err) {
		return Credentials{}, errors.New("Claude CLI credential file is unavailable")
	}
	if runtime.GOOS != "darwin" || !s.defaultDirectory {
		return Credentials{}, credentialstore.ErrNotFound
	}
	// The default Keychain login does not belong to a custom or isolated home.
	current, err := user.Current()
	if err != nil || filepath.Clean(s.home) != filepath.Clean(current.HomeDir) {
		return Credentials{}, credentialstore.ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	for _, args := range [][]string{{"-a", s.account}, nil} {
		commandArgs := append([]string{"find-generic-password", "-s", "Claude Code-credentials", "-w"}, args...)
		cmd := exec.CommandContext(ctx, "/usr/bin/security", commandArgs...)
		var output claudeCommandBuffer
		cmd.Stdout = &output
		err := cmd.Run()
		if err != nil || output.exceeded {
			clear(output.data)
			continue
		}
		data := strings.TrimSpace(string(output.data))
		clear(output.data)
		raw := []byte(data)
		if decoded, err := hex.DecodeString(data); err == nil && json.Valid(decoded) && !strings.HasPrefix(data, "{") {
			clear(raw)
			raw = decoded
		}
		credentials, err := decodeClaudeCredential(raw)
		clear(raw)
		if err == nil {
			return credentials, nil
		}
	}
	return Credentials{}, credentialstore.ErrNotFound
}

func decodeClaudeCredential(raw []byte) (Credentials, error) {
	var payload struct {
		OAuth struct {
			AccessToken      string `json:"accessToken"`
			SubscriptionType string `json:"subscriptionType"`
		} `json:"claudeAiOauth"`
	}
	if json.Unmarshal(raw, &payload) != nil || strings.TrimSpace(payload.OAuth.AccessToken) == "" {
		return Credentials{}, errors.New("Claude CLI credential file is invalid")
	}
	return Credentials{AccessToken: payload.OAuth.AccessToken, SubscriptionType: payload.OAuth.SubscriptionType, Source: "claude-cli"}, nil
}

type claudeCommandBuffer struct {
	data     []byte
	exceeded bool
}

func (b *claudeCommandBuffer) Write(data []byte) (int, error) {
	if len(b.data)+len(data) > 1<<20 {
		b.exceeded = true
		return 0, io.ErrShortWrite
	}
	b.data = append(b.data, data...)
	return len(data), nil
}

var claudeKeychainAccountPattern = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

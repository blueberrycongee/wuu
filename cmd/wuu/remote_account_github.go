package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/blueberrycongee/wuu/internal/remote/account"
	"github.com/blueberrycongee/wuu/internal/remote/host"
	"github.com/blueberrycongee/wuu/internal/remote/secure"
)

type githubPending struct {
	Server    string `json:"server"`
	RequestID string `json:"request_id"`
	Verifier  string `json:"verifier"`
	URL       string `json:"url"`
	Expires   int64  `json:"expires"`
}

func readGithubPending(path string) *githubPending {
	data, err := os.ReadFile(path + ".github-login")
	if err != nil {
		return nil
	}
	var p githubPending
	if json.Unmarshal(data, &p) != nil || p.Expires <= time.Now().Unix() || len(p.RequestID) != 43 || len(p.Verifier) != 43 {
		_ = os.Remove(path + ".github-login")
		return nil
	}
	if _, err := account.Origin(p.Server); err != nil {
		_ = os.Remove(path + ".github-login")
		return nil
	}
	return &p
}
func runGithubAccount(ctx context.Context, path, action, server string, store *host.Store) error {
	if action == "config" {
		var result map[string]any
		if err := account.Request(ctx, server, "", "GET", "/config", nil, &result); err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(result)
	}
	p := readGithubPending(path)
	if action == "github-start" {
		origin, err := account.Origin(server)
		if err != nil {
			return err
		}
		bytes := make([]byte, 32)
		if _, err = rand.Read(bytes); err != nil {
			return err
		}
		verifier := base64.RawURLEncoding.EncodeToString(bytes)
		hash := sha256.Sum256([]byte(verifier))
		var result struct {
			RequestID string `json:"request_id"`
			URL       string `json:"authorize_url"`
		}
		if err = account.Request(ctx, origin, "", "POST", "/github/start", map[string]any{"challenge": base64.RawURLEncoding.EncodeToString(hash[:]), "native": false}, &result); err != nil {
			return err
		}
		target, parseErr := url.Parse(result.URL)
		if parseErr != nil || target.Scheme+"://"+target.Host != origin || target.Path != "/v1/account/github/authorize" || target.User != nil || len(result.RequestID) != 43 {
			return errors.New("invalid GitHub authorization response")
		}
		p = &githubPending{Server: origin, RequestID: result.RequestID, Verifier: verifier, URL: result.URL, Expires: time.Now().Add(10 * time.Minute).Unix()}
		data, _ := json.Marshal(p)
		if err = writeGithubPending(path, data); err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"oauth_url": p.URL})
	}
	if p == nil {
		if action == "github-cancel" {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{})
		}
		return errors.New("GitHub login expired; start again")
	}
	body := map[string]string{"request_id": p.RequestID, "verifier": p.Verifier}
	if action == "github-cancel" {
		_ = account.Request(ctx, p.Server, "", "POST", "/github/cancel", body, nil)
		_ = os.Remove(path + ".github-login")
		return json.NewEncoder(os.Stdout).Encode(map[string]any{})
	}
	var status struct {
		Status   string `json:"status"`
		Username string `json:"username"`
	}
	if err := account.Request(ctx, p.Server, "", "POST", "/github/poll", body, &status); err != nil {
		return err
	}
	if status.Status != "authorized" {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"oauth_url": p.URL})
	}
	id := store.Identity()
	body["pub"] = secure.EncodeKey(id.Public())
	body["role"] = "host"
	body["name"] = store.HostName()
	body["proof"] = base64.RawURLEncoding.EncodeToString(id.SignRelayAuth([]byte("wuu/account/enroll/v1:"+status.Username), "host"))
	var session account.Session
	if err := account.Request(ctx, p.Server, "", "POST", "/github/complete", body, &session); err != nil {
		return err
	}
	if session.Username != status.Username || session.Pub != body["pub"] {
		return errors.New("invalid account enrollment response")
	}
	if err := store.SetRelayURL("ws" + p.Server[4:] + "/v1/connect"); err != nil {
		return err
	}
	if err := store.SetAccount(&account.Credentials{Server: p.Server, Token: session.Token, Username: session.Username}); err != nil {
		return err
	}
	_ = os.Remove(path + ".github-login")
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"username": session.Username, "server": p.Server, "auth_method": "github"})
}

func writeGithubPending(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".github-login-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path+".github-login")
}

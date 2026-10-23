package grokbuild

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCredentialSourceUsesExplicitTokenFirst(t *testing.T) {
	t.Setenv("GROK_HOME", filepath.Join(t.TempDir(), "missing"))
	token, err := (credentialSource{explicitToken: " explicit ", reuseCLI: true}).token(context.Background())
	if err != nil || token != "explicit" {
		t.Fatalf("token = %q, err = %v", token, err)
	}
}

func TestLoadCLITokenUsesGrokHomeAndCurrentOAuthScope(t *testing.T) {
	grokHome := t.TempDir()
	t.Setenv("GROK_HOME", grokHome)
	writeAuthFile(t, grokHome, `{"https://auth.x.ai::b1a00492-073a-47ea-816f-4c329264a828":{"key":"cli-token","token":"fallback"}}`)
	token, err := loadCLIToken(t.TempDir())
	if err != nil || token != "cli-token" {
		t.Fatalf("token = %q, err = %v", token, err)
	}
	if source, err := LocalOAuthStatus(t.TempDir()); err != nil || source != "grok-cli" {
		t.Fatalf("LocalOAuthStatus = %q, %v", source, err)
	}
}

func TestLoadCLITokenFallsBackToHome(t *testing.T) {
	t.Setenv("GROK_HOME", "")
	home := t.TempDir()
	writeAuthFile(t, filepath.Join(home, ".grok"), `{"https://accounts.x.ai/sign-in":{"key":"home-token"}}`)
	token, err := loadCLIToken(home)
	if err != nil || token != "home-token" {
		t.Fatalf("token = %q, err = %v", token, err)
	}
}

func TestLoadCLITokenPrefersCurrentOAuthScopeOverLegacy(t *testing.T) {
	grokHome := t.TempDir()
	t.Setenv("GROK_HOME", grokHome)
	writeAuthFile(t, grokHome, `{
		"https://accounts.x.ai/sign-in":{"key":"legacy-token"},
		"https://auth.x.ai::b1a00492-073a-47ea-816f-4c329264a828":{"key":"oauth-token"}
	}`)
	token, err := loadCLIToken(t.TempDir())
	if err != nil || token != "oauth-token" {
		t.Fatalf("token = %q, err = %v", token, err)
	}
}

func TestCredentialSourceRejectsDisabledReuse(t *testing.T) {
	_, err := (credentialSource{}).token(context.Background())
	if err == nil || !strings.Contains(err.Error(), "reuse_grok_credentials") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadCLITokenRejectsMissingIssuerOrToken(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{name: "issuer", body: `{"other":{"key":"token"}}`, want: "do not contain"},
		{name: "token", body: `{"https://accounts.x.ai/sign-in":{}}`, want: "token is empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			grokHome := t.TempDir()
			t.Setenv("GROK_HOME", grokHome)
			writeAuthFile(t, grokHome, tc.body)
			_, err := loadCLIToken(t.TempDir())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func writeAuthFile(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

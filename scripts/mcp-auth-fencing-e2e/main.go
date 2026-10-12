// Command mcp-auth-fencing-e2e checks OAuth publication using two independent
// processes, one shared synthetic store, and loopback-only OAuth/resource URLs.
// Run: go run ./scripts/mcp-auth-fencing-e2e
package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/mcp"
	keyring "github.com/zalando/go-keyring"
)

type message struct {
	Op            string `json:"op,omitempty"`
	Resource      string `json:"resource,omitempty"`
	Code          string `json:"code,omitempty"`
	State         string `json:"state,omitempty"`
	Gate          bool   `json:"gate,omitempty"`
	BackendGate   bool   `json:"backend_gate,omitempty"`
	Kind          string `json:"kind,omitempty"`
	Token         string `json:"token,omitempty"`
	Authenticated bool   `json:"authenticated,omitempty"`
	Error         string `json:"error,omitempty"`
	Required      bool   `json:"required,omitempty"`
}

type atomicStore interface {
	Update(context.Context, string, string, func([]byte) ([]byte, error)) error
}

type gatedStore struct {
	credentialstore.Store
	input  *json.Decoder
	output *json.Encoder
	gate   bool
}

func (s *gatedStore) barrier() error {
	if !s.gate {
		return nil
	}
	s.gate = false
	if err := s.output.Encode(message{Kind: "commit-ready"}); err != nil {
		return err
	}
	var release message
	if err := s.input.Decode(&release); err != nil {
		return err
	}
	if release.Op != "release" {
		return errors.New("expected release")
	}
	return nil
}

// Intercept Set too, so this same E2E can demonstrate the pre-fix read/write gap.
func (s *gatedStore) Set(ctx context.Context, ns, key string, data []byte) error {
	if err := s.barrier(); err != nil {
		return err
	}
	return s.Store.Set(ctx, ns, key, data)
}

func (s *gatedStore) Update(ctx context.Context, ns, key string, fn func([]byte) ([]byte, error)) error {
	if err := s.barrier(); err != nil {
		return err
	}
	return s.Store.(atomicStore).Update(ctx, ns, key, fn)
}

type syntheticKeyring struct {
	backing *credentialstore.FileStore
	path    string
	output  *json.Encoder
	gate    bool
}

func (s *syntheticKeyring) Get(service, account string) (string, error) {
	data, err := s.backing.Get(context.Background(), service, account)
	if errors.Is(err, credentialstore.ErrNotFound) {
		return "", keyring.ErrNotFound
	}
	return string(data), err
}
func (s *syntheticKeyring) Set(service, account, value string) error {
	if s.gate {
		s.gate = false
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		// Model a native backend command that has consumed its input but can
		// outlive the caller. No real Keychain or account is involved.
		cmd := exec.Command(executable, "orphan-keyring-write", s.path, service, account)
		cmd.Stdin, cmd.Stderr = strings.NewReader(value), os.Stderr
		if err := cmd.Start(); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := waitFile(ctx, s.path+".orphan-ready"); err != nil {
			return err
		}
		if err := s.output.Encode(message{Kind: "backend-write-started"}); err != nil {
			return err
		}
		return cmd.Wait()
	}
	return s.backing.Set(context.Background(), service, account, []byte(value))
}
func (s *syntheticKeyring) Delete(service, account string) error {
	return s.backing.Delete(context.Background(), service, account)
}

func waitFile(ctx context.Context, path string) error {
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func worker(path, mode string) {
	input, output := json.NewDecoder(os.Stdin), json.NewEncoder(os.Stdout)
	var backing credentialstore.Store = credentialstore.NewFileStore(path)
	var backend *syntheticKeyring
	if mode == "keyring" {
		backend = &syntheticKeyring{backing: credentialstore.NewFileStore(path), path: path, output: output}
		backing = credentialstore.NewKeyringStore("wuu-synthetic-e2e", backend)
	}
	store := &gatedStore{Store: backing, input: input, output: output}
	oauth := mcp.NewOAuthManager(store, &http.Client{Timeout: 10 * time.Second})
	for {
		var request message
		if err := input.Decode(&request); err != nil {
			if err == io.EOF {
				return
			}
			panic(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		result := message{Kind: "result"}
		store.gate = request.Gate
		if backend != nil {
			backend.gate = request.BackendGate
		}
		var err error
		switch request.Op {
		case "seed-legacy":
			var data []byte
			data, err = json.Marshal(map[string]any{"access_token": "legacy-account", "refresh_token": "synthetic-rotation", "client_id": "synthetic-client", "token_endpoint": request.Code, "resource_url": request.Resource})
			if err == nil {
				err = backend.Set("wuu-synthetic-e2e", "mcp:docs:oauth", base64.StdEncoding.EncodeToString(data))
			}
		case "legacy-orphan":
			var data []byte
			data, err = backing.Get(ctx, "mcp:docs", "oauth")
			if err == nil {
				backend.gate = true
				err = backend.Set("wuu-synthetic-e2e", "mcp:docs:oauth", base64.StdEncoding.EncodeToString(data))
			}
		case "hold-store", "increment", "increment-in-place", "contend-store":
			if request.Op == "contend-store" {
				var boundedCancel context.CancelFunc
				ctx, boundedCancel = context.WithTimeout(ctx, 50*time.Millisecond)
				defer boundedCancel()
			}
			err = store.Store.(atomicStore).Update(ctx, "synthetic-counter", "value", func(data []byte) ([]byte, error) {
				if request.Op == "hold-store" {
					if err := output.Encode(message{Kind: "lock-held"}); err != nil {
						return nil, err
					}
					var release message
					if err := input.Decode(&release); err != nil {
						return nil, err
					}
					if release.Op == "crash" {
						os.Exit(0)
					}
					if release.Op != "release" {
						return nil, errors.New("expected release")
					}
				}
				value := 0
				if len(data) > 0 {
					var err error
					value, err = strconv.Atoi(string(data))
					if err != nil {
						return nil, err
					}
				}
				result.Token = strconv.Itoa(value + 1)
				if request.Op == "increment-in-place" {
					if len(data) != len(result.Token) {
						return nil, errors.New("in-place increment changed length")
					}
					copy(data, result.Token)
					return data, nil
				}
				return []byte(result.Token), nil
			})
		case "make-legacy":
			err = store.Store.(atomicStore).Update(ctx, "mcp:docs", "oauth", func(data []byte) ([]byte, error) {
				var record map[string]any
				if err := json.Unmarshal(data, &record); err != nil {
					return nil, err
				}
				delete(record, "login_generation")
				delete(record, "credential_version")
				return json.Marshal(record)
			})
		case "start":
			var start mcp.OAuthStartResult
			start, err = oauth.Start(ctx, mcp.OAuthStartOptions{ServerID: "docs", ResourceURL: request.Resource, RedirectURI: "http://127.0.0.1/callback", ClientID: "synthetic-client"})
			result.State = start.State
		case "finish":
			var status mcp.OAuthStatus
			status, err = oauth.Finish(ctx, mcp.OAuthFinishOptions{ServerID: "docs", ResourceURL: request.Resource, State: request.State, Code: request.Code})
			result.Authenticated = status.Authenticated
		case "refresh":
			result.Token, err = oauth.RefreshAccessToken(ctx, "docs", request.Resource)
		case "remove":
			err = oauth.Remove(ctx, "docs")
		case "delete-slot":
			err = backing.Delete(ctx, "mcp:docs", "oauth")
		case "access":
			result.Token, err = oauth.AccessToken(ctx, "docs", request.Resource)
		case "status":
			var status mcp.OAuthStatus
			status, err = oauth.Status(ctx, "docs", request.Resource)
			result.Authenticated = status.Authenticated
		default:
			err = fmt.Errorf("unknown operation %q", request.Op)
		}
		cancel()
		if err != nil {
			result.Error = err.Error()
			result.Required = errors.Is(err, mcp.ErrOAuthRequired)
		}
		if err := output.Encode(result); err != nil {
			panic(err)
		}
	}
}

type process struct {
	cmd    *exec.Cmd
	in     io.WriteCloser
	out    *json.Decoder
	path   string
	killed bool
}

func launch(ctx context.Context, path, mode string) *process {
	executable, err := os.Executable()
	must(err)
	cmd := exec.CommandContext(ctx, executable, "worker", path, mode)
	cmd.Env = append(os.Environ(), "HOME="+filepath.Dir(path), "USERPROFILE="+filepath.Dir(path), "WUU_HOME="+path+fmt.Sprint(time.Now().UnixNano()))
	in, err := cmd.StdinPipe()
	must(err)
	out, err := cmd.StdoutPipe()
	must(err)
	cmd.Stderr = os.Stderr
	must(cmd.Start())
	return &process{cmd: cmd, in: in, out: json.NewDecoder(bufio.NewReader(out)), path: path}
}
func (p *process) send(m message) { must(json.NewEncoder(p.in).Encode(m)) }
func (p *process) read() message  { var m message; must(p.out.Decode(&m)); return m }
func (p *process) call(m message) message {
	p.send(m)
	result := p.read()
	check(result.Kind == "result", "unexpected worker event")
	return result
}
func (p *process) close() {
	_ = p.in.Close()
	err := p.cmd.Wait()
	if !p.killed {
		must(err)
	}
}
func (p *process) kill() { p.killed = true; must(p.cmd.Process.Kill()) }
func must(err error) {
	if err != nil {
		panic(err)
	}
}
func check(ok bool, text string) {
	if !ok {
		panic(text)
	}
}
func success(m message) message { check(m.Error == "", m.Error); return m }
func start(p *process, resource string) string {
	return success(p.call(message{Op: "start", Resource: resource})).State
}
func finish(p *process, resource, state, code string) {
	success(p.call(message{Op: "finish", Resource: resource, State: state, Code: code}))
}
func login(p *process, resource, code string) { finish(p, resource, start(p, resource), code) }
func absent(p *process, resource string) {
	r := success(p.call(message{Op: "status", Resource: resource}))
	check(!r.Authenticated, "credentials were resurrected")
}
func token(p *process, resource, expected string) {
	r := success(p.call(message{Op: "access", Resource: resource}))
	check(r.Token == expected, "replacement credential was overwritten")
}
func gate(p *process, m message) {
	m.Gate = true
	p.send(m)
	check(p.read().Kind == "commit-ready", "worker did not reach conditional-write boundary")
}
func release(p *process) message { p.send(message{Op: "release"}); return p.read() }

func main() {
	if len(os.Args) == 5 && os.Args[1] == "orphan-keyring-write" {
		path, service, account := os.Args[2], os.Args[3], os.Args[4]
		data, err := io.ReadAll(os.Stdin)
		must(err)
		must(os.WriteFile(path+".orphan-ready", nil, 0o600))
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		must(waitFile(ctx, path+".orphan-release"))
		must(credentialstore.NewFileStore(path).Set(ctx, service, account, data))
		must(os.WriteFile(path+".orphan-done", nil, 0o600))
		return
	}
	if len(os.Args) == 4 && os.Args[1] == "worker" {
		worker(os.Args[2], os.Args[3])
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	root, err := os.MkdirTemp("", "wuu-mcp-auth-e2e-")
	must(err)
	defer os.RemoveAll(root)
	var base string
	var mu sync.Mutex
	var observed []string
	tokenRequests := 0
	var heldPath string
	var entered, releaseNetwork chan struct{}
	blockNetwork := func(path string) {
		mu.Lock()
		defer mu.Unlock()
		heldPath, entered, releaseNetwork = path, make(chan struct{}, 1), make(chan struct{})
	}
	awaitNetwork := func() {
		select {
		case <-entered:
		case <-ctx.Done():
			panic("network barrier timed out")
		}
	}
	unblockNetwork := func() { mu.Lock(); defer mu.Unlock(); heldPath = ""; close(releaseNetwork) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		blocked, arrived, release := r.URL.Path == heldPath, entered, releaseNetwork
		mu.Unlock()
		if blocked {
			arrived <- struct{}{}
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		if strings.HasPrefix(r.URL.Path, "/.well-known/oauth-protected-resource") {
			resource := base + strings.TrimPrefix(r.URL.Path, "/.well-known/oauth-protected-resource")
			_ = json.NewEncoder(w).Encode(map[string]any{"resource": resource, "authorization_servers": []string{base}})
			return
		}
		if r.URL.Path == "/.well-known/oauth-authorization-server" {
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": base, "authorization_endpoint": base + "/authorize", "token_endpoint": base + "/token"})
			return
		}
		if r.URL.Path == "/token" {
			_ = r.ParseForm()
			mu.Lock()
			tokenRequests++
			mu.Unlock()
			access := r.Form.Get("code")
			if r.Form.Get("grant_type") == "refresh_token" {
				access = "refreshed-" + r.Form.Get("refresh_token")
			}
			expiry := 3600
			if access == "same-token" {
				expiry = 0
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": access, "refresh_token": "synthetic-rotation", "token_type": "Bearer", "expires_in": expiry})
			return
		}
		if auth := r.Header.Get("Authorization"); auth != "" {
			mu.Lock()
			observed = append(observed, r.URL.Path+" "+auth)
			mu.Unlock()
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	base = server.URL
	run := func(name string, scenario func(*process, *process), modes ...string) {
		if len(modes) == 0 {
			modes = []string{"file", "keyring"}
		}
		for _, mode := range modes {
			func() {
				path := filepath.Join(root, name+"-"+mode, "synthetic.json")
				a, b := launch(ctx, path, mode), launch(ctx, path, mode)
				defer a.close()
				defer b.close()
				scenario(a, b)
				_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"case": name, "backend": mode, "status": "passed", "processes": []int{a.cmd.Process.Pid, b.cmd.Process.Pid}})
			}()
		}
	}
	run("refresh-replacement-resource", func(a, b *process) {
		login(a, base+"/a", "account-a")
		gate(a, message{Op: "refresh", Resource: base + "/a"})
		login(b, base+"/b", "account-b")
		check(release(a).Required, "old-resource refresh was accepted")
		token(b, base+"/b", "account-b")
		denied := a.call(message{Op: "access", Resource: base + "/a"})
		check(denied.Required && denied.Token == "", "old resource retained new credential")
		bearer := success(b.call(message{Op: "access", Resource: base + "/b"})).Token
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/b", nil)
		must(err)
		req.Header.Set("Authorization", "Bearer "+bearer)
		response, err := server.Client().Do(req)
		must(err)
		_ = response.Body.Close()
	})
	run("refresh-replacement-account", func(a, b *process) {
		login(a, base+"/a", "old-account")
		gate(a, message{Op: "refresh", Resource: base + "/a"})
		login(b, base+"/a", "new-account")
		check(success(release(a)).Token == "new-account", "refresh did not adopt same-resource winner")
		token(b, base+"/a", "new-account")
	})
	run("same-token-login-still-replaces-generation", func(a, b *process) {
		login(a, base+"/a", "same-token")
		gate(a, message{Op: "refresh", Resource: base + "/a"})
		login(b, base+"/a", "same-token")
		check(success(release(a)).Token == "same-token", "refresh overwrote a same-token new login")
		token(b, base+"/a", "same-token")
	})
	run("refresh-logout", func(a, b *process) {
		login(a, base+"/a", "old-account")
		gate(a, message{Op: "refresh", Resource: base + "/a"})
		success(b.call(message{Op: "remove"}))
		absent(b, base+"/a")
		check(release(a).Required, "refresh crossed logout")
		absent(a, base+"/a")
		login(b, base+"/a", "fresh-login")
		token(a, base+"/a", "fresh-login")
	})
	run("finish-logout", func(a, b *process) {
		state := start(a, base+"/a")
		gate(a, message{Op: "finish", Resource: base + "/a", State: state, Code: "late-code"})
		success(b.call(message{Op: "remove"}))
		check(release(a).Required, "Finish crossed logout")
		absent(a, base+"/a")
		login(b, base+"/a", "fresh-login")
		token(a, base+"/a", "fresh-login")
	})
	run("logout-during-refresh-network", func(a, b *process) {
		login(a, base+"/a", "old-account")
		blockNetwork("/token")
		a.send(message{Op: "refresh", Resource: base + "/a"})
		awaitNetwork()
		success(b.call(message{Op: "remove"}))
		unblockNetwork()
		check(a.read().Required, "refresh network response crossed logout")
		absent(a, base+"/a")
	})
	run("logout-during-finish-network", func(a, b *process) {
		state := start(a, base+"/a")
		blockNetwork("/token")
		a.send(message{Op: "finish", Resource: base + "/a", State: state, Code: "late-code"})
		awaitNetwork()
		success(b.call(message{Op: "remove"}))
		unblockNetwork()
		check(a.read().Required, "Finish network response crossed logout")
		absent(a, base+"/a")
	})
	run("logout-during-start-discovery", func(a, b *process) {
		blockNetwork("/.well-known/oauth-protected-resource/a")
		a.send(message{Op: "start", Resource: base + "/a"})
		awaitNetwork()
		success(b.call(message{Op: "remove"}))
		unblockNetwork()
		check(a.read().Required, "Start discovery crossed logout")
		absent(a, base+"/a")
	})
	run("pending-callback-logout", func(a, b *process) {
		state := start(a, base+"/a")
		success(b.call(message{Op: "remove"}))
		mu.Lock()
		before := tokenRequests
		mu.Unlock()
		check(a.call(message{Op: "finish", Resource: base + "/a", State: state, Code: "old-browser-code"}).Error != "", "stale browser callback was accepted")
		mu.Lock()
		after := tokenRequests
		mu.Unlock()
		check(before == after, "revoked browser code reached token endpoint")
		absent(a, base+"/a")
	})
	run("last-start-wins", func(a, b *process) {
		older := start(a, base+"/a")
		newer := start(b, base+"/a")
		finish(b, base+"/a", newer, "winner")
		check(a.call(message{Op: "finish", Resource: base + "/a", State: older, Code: "loser"}).Required, "older login superseded latest Start")
		token(a, base+"/a", "winner")
	})
	run("refresh-does-not-revoke-pending-login", func(a, b *process) {
		login(a, base+"/a", "old-account")
		state := start(b, base+"/a")
		success(a.call(message{Op: "refresh", Resource: base + "/a"}))
		finish(b, base+"/a", state, "new-account")
		token(a, base+"/a", "new-account")
	})
	run("concurrent-refresh-winner", func(a, b *process) {
		login(a, base+"/a", "old-account")
		gate(a, message{Op: "refresh", Resource: base + "/a"})
		winner := success(b.call(message{Op: "refresh", Resource: base + "/a"})).Token
		check(success(release(a)).Token == winner, "late refresh did not adopt committed winner")
		token(a, base+"/a", winner)
	})
	run("atomic-store-exclusion-and-bounded-wait", func(a, b *process) {
		a.send(message{Op: "hold-store"})
		check(a.read().Kind == "lock-held", "writer did not acquire store lock")
		contender := b.call(message{Op: "contend-store"})
		check(contender.Error != "", "another process entered locked mutation")
		check(success(release(a)).Token == "1", "first atomic mutation failed")
		check(success(b.call(message{Op: "increment"})).Token == "2", "lock was not released or counter was overwritten")
	})
	run("atomic-store-process-exit-releases-lock", func(a, b *process) {
		a.send(message{Op: "hold-store"})
		check(a.read().Kind == "lock-held", "writer did not acquire store lock")
		a.send(message{Op: "crash"})
		check(success(b.call(message{Op: "increment"})).Token == "1", "exited writer left a stale lock or committed its callback")
	})
	run("legacy-credential-refresh-and-login", func(a, b *process) {
		login(a, base+"/a", "legacy-account")
		success(a.call(message{Op: "make-legacy"}))
		token(b, base+"/a", "legacy-account")
		gate(a, message{Op: "refresh", Resource: base + "/a"})
		login(b, base+"/a", "new-account")
		check(success(release(a)).Token == "new-account", "legacy refresh overwrote a newer login")
		token(a, base+"/a", "new-account")
	})
	run("atomic-store-in-place-update", func(a, b *process) {
		check(success(a.call(message{Op: "increment"})).Token == "1", "counter initialization failed")
		check(success(a.call(message{Op: "increment-in-place"})).Token == "2", "in-place callback failed")
		check(success(b.call(message{Op: "increment"})).Token == "3", "in-place Update was silently discarded")
	})
	for _, replacement := range []string{"logout", "new-login", "legacy-logout", "delete"} {
		run("orphan-backend-write-after-"+replacement, func(a, b *process) {
			if replacement == "legacy-logout" {
				login(a, base+"/a", "old-account")
				a.send(message{Op: "legacy-orphan"})
			} else {
				state := start(a, base+"/a")
				a.send(message{Op: "finish", Resource: base + "/a", State: state, Code: "old-account", BackendGate: true})
			}
			check(a.read().Kind == "backend-write-started", "native-like backend did not start")
			a.kill()
			if replacement == "new-login" {
				login(b, base+"/a", "new-account")
				token(b, base+"/a", "new-account")
			} else if replacement == "delete" {
				success(b.call(message{Op: "delete-slot"}))
			} else {
				success(b.call(message{Op: "remove"}))
			}
			must(os.WriteFile(a.path+".orphan-release", nil, 0o600))
			must(waitFile(ctx, a.path+".orphan-done"))
			absent(b, base+"/a")
			denied := b.call(message{Op: "access", Resource: base + "/a"})
			check(denied.Error != "" && denied.Token == "", "orphaned native write restored bearer authority")
			login(b, base+"/a", "recovered-account")
			token(b, base+"/a", "recovered-account")
		}, "keyring")
	}
	run("legacy-keyring-envelope-migration", func(a, b *process) {
		success(a.call(message{Op: "seed-legacy", Resource: base + "/a", Code: base + "/token"}))
		token(b, base+"/a", "legacy-account")
		check(success(b.call(message{Op: "refresh", Resource: base + "/a"})).Token == "refreshed-synthetic-rotation", "legacy Keyring credential could not migrate")
		token(a, base+"/a", "refreshed-synthetic-rotation")
		success(b.call(message{Op: "remove"}))
		absent(a, base+"/a")
	}, "keyring")
	run("keyring-authority-publication-failure", func(a, b *process) {
		login(a, base+"/a", "old-account")
		state := start(a, base+"/a")
		a.send(message{Op: "finish", Resource: base + "/a", State: state, Code: "unpublished-account", BackendGate: true})
		check(a.read().Kind == "backend-write-started", "backend did not reach authority publication boundary")
		directory := filepath.Join(filepath.Dir(a.path), ".wuu", "credential-locks")
		must(os.Rename(directory, directory+".saved"))
		must(os.WriteFile(directory, nil, 0o600))
		must(os.WriteFile(a.path+".orphan-release", nil, 0o600))
		result := a.read()
		check(strings.Contains(result.Error, "revision write"), "authority publication failure was not reported")
		denied := b.call(message{Op: "access", Resource: base + "/a"})
		check(denied.Error != "" && denied.Token == "", "authority I/O failure fell back to backend credentials")
		must(os.Remove(directory))
		must(os.Rename(directory+".saved", directory))
		absent(b, base+"/a")
		login(b, base+"/a", "recovered-account")
		token(b, base+"/a", "recovered-account")
	}, "keyring")
	run("keyring-authority-missing-invalid-or-unsafe", func(a, b *process) {
		login(a, base+"/a", "current-account")
		paths, err := filepath.Glob(filepath.Join(filepath.Dir(a.path), ".wuu", "credential-locks", "*.revision"))
		must(err)
		check(len(paths) == 1, "expected one authority record")
		path := paths[0]
		data, err := os.ReadFile(path)
		must(err)
		check(!strings.Contains(string(data), "current-account") && !strings.Contains(string(data), "synthetic-rotation"), "authority record contains credentials")
		must(os.Remove(path))
		absent(b, base+"/a")
		must(os.WriteFile(path, []byte("invalid"), 0o600))
		check(b.call(message{Op: "access", Resource: base + "/a"}).Error != "", "corrupt authority was ignored")
		must(os.WriteFile(path, data, 0o600))
		if runtime.GOOS != "windows" {
			must(os.Chmod(path, 0o644))
			check(b.call(message{Op: "access", Resource: base + "/a"}).Error != "", "unsafe authority permissions were ignored")
			must(os.Chmod(path, 0o600))
			must(os.Rename(path, path+".saved"))
			must(os.Symlink(path+".saved", path))
			check(b.call(message{Op: "access", Resource: base + "/a"}).Error != "", "symlink authority was followed")
			must(os.Remove(path))
			must(os.Rename(path+".saved", path))
		}
		token(b, base+"/a", "current-account")
	}, "keyring")
	mu.Lock()
	defer mu.Unlock()
	for _, pair := range observed {
		check(pair != "/b Bearer account-a", "resource B received A's bearer")
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"status": "passed", "resource_headers": observed, "token_requests": tokenRequests, "credentials": "synthetic only; no Keychain or external account access"})
}

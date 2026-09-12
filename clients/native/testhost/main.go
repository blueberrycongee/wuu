// Command testhost serves the real relay and execution host for native client tests.
// All identities and state are disposable. It never reads the user's Wuu configuration.
package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/hooks"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/remote/host"
	"github.com/blueberrycongee/wuu/internal/remote/relay"
	"github.com/blueberrycongee/wuu/internal/remote/secure"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/tools"
)

type provider struct{ release chan struct{} }

func (p *provider) Chat(ctx context.Context, _ providers.ChatRequest) (providers.ChatResponse, error) {
	select {
	case <-p.release:
		return providers.ChatResponse{Content: "native transport verified"}, nil
	case <-ctx.Done():
		return providers.ChatResponse{}, ctx.Err()
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	root, err := os.MkdirTemp("", "wuu-native-test-")
	must(err)
	defer os.RemoveAll(root)
	must(os.Setenv("HOME", root))
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	registry, err := relay.OpenRegistry("")
	must(err)
	server := httptest.NewServer(relay.New(relay.Options{Registry: registry}).Handler())
	defer server.Close()
	store, err := host.LoadOrCreateStore(filepath.Join(root, "host.json"), "Native test computer")
	must(err)
	phone, err := secure.NewIdentity()
	must(err)
	b64 := base64.RawURLEncoding.EncodeToString
	must(store.AddDevice(phone.Public(), "Native test phone", time.Now()))
	must(registry.AddDevice(b64(store.Identity().Public()), b64(phone.Public()), "Native test phone", time.Now()))
	kit, err := tools.New(root)
	must(err)
	gate := &provider{release: make(chan struct{}, 8)}
	rt := &runtime.Session{
		ProviderName: "native-test", Model: "native-test", RootDir: root,
		ConfigPath: filepath.Join(root, "config.json"), SessionDir: filepath.Join(root, "sessions"),
		HookDispatcher: hooks.NewDispatcher(nil), Toolkit: kit, UserQuestions: pluginhost.NewUserQuestionBroker(),
		StreamRunner: &agent.StreamRunner{Client: providers.AdaptStreamClient(gate), Model: "native-test", SystemPrompt: "test"},
	}
	ready := make(chan struct{}, 1)
	h, err := host.New(host.Options{Runtime: rt, Store: store,
		RelayURL: "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/connect",
		Pairing: &host.PairingConfig{Once: true, OnURI: func(string) {
			select {
			case ready <- struct{}{}:
			default:
			}
		}},
	})
	must(err)
	go func() {
		if err := h.Run(ctx); err != nil && ctx.Err() == nil {
			fmt.Fprintln(os.Stderr, err)
			cancel()
		}
	}()
	select {
	case <-ready:
	case <-ctx.Done():
		panic(ctx.Err())
	}
	must(json.NewEncoder(os.Stdout).Encode(map[string]string{
		"server": server.URL, "token": "test-only", "username": "native-test",
		"pub": b64(phone.Public()), "deviceSeed": b64(phone.Seed()), "seed": b64(phone.Seed()),
		"host": b64(store.Identity().Public()), "workspace": root,
	}))
	commands := make(chan string)
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			commands <- scanner.Text()
		}
		close(commands)
	}()
	for {
		select {
		case command, ok := <-commands:
			if !ok || command == "quit" {
				return
			}
			if command == "release" {
				gate.release <- struct{}{}
			}
		case <-ctx.Done():
			return
		}
	}
}

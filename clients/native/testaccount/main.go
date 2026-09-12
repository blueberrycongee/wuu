// Command testaccount exposes the production account handler over loopback HTTP.
// It requires a disposable PostgreSQL database and drops its private schema on exit.
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/remote/account"
	"github.com/blueberrycongee/wuu/internal/remote/host"
	"github.com/blueberrycongee/wuu/internal/remote/relay"
	"github.com/blueberrycongee/wuu/internal/remote/secure"
	"github.com/jackc/pgx/v5"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	github := flag.Bool("github", false, "enable a local simulated GitHub provider")
	live := flag.Bool("live", false, "start an isolated execution computer for simulator UI tests")
	flag.Parse()
	if *github && *live {
		panic("-github and -live are separate fixture modes")
	}
	var challenges *sync.Map
	if *github {
		challenges = fakeGitHub()
	}
	dsn := os.Getenv("WUU_TEST_DATABASE_URL")
	if dsn == "" {
		panic("WUU_TEST_DATABASE_URL must name a disposable database")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	must(err)
	defer conn.Close(ctx)
	schema := fmt.Sprintf("native_%x", rand.Text())
	identifier := pgx.Identifier{schema}.Sanitize()
	_, err = conn.Exec(ctx, "CREATE SCHEMA "+identifier)
	must(err)
	defer func() { _, err := conn.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE"); must(err) }()
	u, err := url.Parse(dsn)
	must(err)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	store, err := account.Open(u.String())
	must(err)
	defer store.Close()
	id, err := secure.NewIdentity()
	must(err)
	var computer *host.Store
	var root string
	name := "Offline test computer"
	if *live {
		root, err = os.MkdirTemp("", "wuu-native-ui-")
		must(err)
		defer os.RemoveAll(root)
		must(os.Setenv("HOME", root))
		name = "UI test computer"
		computer, err = host.LoadOrCreateStore(filepath.Join(root, "host.json"), name)
		must(err)
		id = computer.Identity()
	}
	const user, password = "native-test", "native-test-password"
	hostSession, err := store.Login(account.Login{Username: user, Password: password, Pub: secure.EncodeKey(id.Public()),
		Role: "host", Name: name, Proof: secure.EncodeKey(id.SignRelayAuth([]byte("wuu/account/enroll/v1:"+user), "host"))}, true)
	must(err)
	handler := account.NewHTTP(store, true)
	// Registration exercises the production handler without contacting APNs or FCM.
	handler.PushPlatforms = []string{"ios", "android"}
	var routes http.Handler = handler
	if *live {
		r := relay.New(relay.Options{Accounts: store, AllowRegistration: true, Logf: func(string, ...any) {}})
		defer r.Close()
		routes = r.Handler()
	}
	// A delayed, already-produced history body reproduces logout/deletion races
	// without sleeps or changes to the production server.
	var mu sync.Mutex
	var gate chan struct{}
	armed := false
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		pause := armed && r.Method == "GET" && r.URL.Path == "/v1/account/history/thread"
		if pause {
			armed = false
		}
		wait := gate
		mu.Unlock()
		if !pause {
			routes.ServeHTTP(w, r)
			return
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, r)
		fmt.Println("blocked")
		select {
		case <-wait:
		case <-r.Context().Done():
			return
		}
		for k, v := range recorder.Header() {
			w.Header()[k] = v
		}
		w.WriteHeader(recorder.Code)
		_, _ = w.Write(recorder.Body.Bytes())
	}))
	if *github {
		handler.GitHub, err = account.NewGitHubAuth(account.GitHubConfig{ClientID: "fixture-client", ClientSecret: "fixture-only-secret", PublicURL: "http://" + server.Listener.Addr().String()})
		must(err)
	}
	server.Start()
	defer server.Close()
	if *live {
		stop := startUIComputer(root, computer, server.URL, hostSession)
		defer stop()
	}
	must(json.NewEncoder(os.Stdout).Encode(map[string]string{"server": server.URL, "username": user, "password": password,
		"host": hostSession.Pub, "hostToken": hostSession.Token}))
	commands := make(chan string)
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			commands <- scanner.Text()
		}
		close(commands)
	}()
	defer func() {
		mu.Lock()
		defer mu.Unlock()
		if gate != nil {
			close(gate)
		}
	}()
	lifetime := 3 * time.Minute
	if *live {
		lifetime = 30 * time.Minute
	}
	timer := time.NewTimer(lifetime)
	defer timer.Stop()
	for {
		select {
		case command, ok := <-commands:
			if !ok || command == "quit" {
				return
			}
			if parts := strings.Fields(command); len(parts) == 2 && challenges != nil && (parts[0] == "authorize" || parts[0] == "deny") {
				must(authorizeBrowser(server.URL, parts[1], parts[0] == "deny", challenges))
				fmt.Println("browser-returned")
				continue
			}
			mu.Lock()
			switch command {
			case "pause-history":
				if gate == nil {
					gate = make(chan struct{})
					armed = true
				}
				fmt.Println("paused")
			case "release-history":
				if gate != nil {
					close(gate)
					gate = nil
				}
				armed = false
			}
			mu.Unlock()
		case <-timer.C:
			return
		}
	}
}

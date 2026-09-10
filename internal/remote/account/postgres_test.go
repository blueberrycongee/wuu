package account

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/remote/pgtest"
)

func TestLoginRechecksPasswordAfterConcurrentResetCommits(t *testing.T) {
	s, err := Open(pgtest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	in := loginInput(t, "alice", "host")
	if _, err := s.Login(in, true); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	reset, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reset.Rollback()
	var pid int
	if err := reset.QueryRowContext(ctx, `SELECT pg_backend_pid() FROM accounts WHERE username='alice' FOR UPDATE`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { _, err := s.Login(in, false); finished <- err }()
	// Observe the database lock wait, so the password work is complete before
	// committing the password reset. No timing assumptions about Argon2 are needed.
	for {
		var waiting bool
		if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, pid).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-finished:
			t.Fatalf("login passed the account lock: %v", err)
		default:
		}
	}
	if _, err := reset.ExecContext(ctx, `UPDATE accounts SET salt=$1,password=$2 WHERE username='alice'`, []byte("0123456789abcdef"), passwordHash("different safe password", []byte("0123456789abcdef"))); err != nil {
		t.Fatal(err)
	}
	if _, err := reset.ExecContext(ctx, `DELETE FROM devices WHERE account='alice'`); err != nil {
		t.Fatal(err)
	}
	if err := reset.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("old password accepted after reset: %v", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestConcurrentDeviceLoginKeepsOneSession(t *testing.T) {
	s, err := Open(pgtest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	in := loginInput(t, "alice", "phone")
	if _, err := s.Login(in, true); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	tokens := make(chan string, 3)
	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() {
			<-start
			session, err := s.Login(in, false)
			if err != nil {
				t.Error(err)
				return
			}
			tokens <- session.Token
		})
	}
	close(start)
	wg.Wait()
	close(tokens)
	valid := 0
	for token := range tokens {
		if _, err := s.Authenticate(token); err == nil {
			valid++
		}
	}
	if valid != 1 {
		t.Fatalf("got %d valid sessions for one device", valid)
	}
}

func TestConcurrentRecoveryAndLoginCannotRestoreOldCredentials(t *testing.T) {
	s, err := Open(pgtest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	in := loginInput(t, "alice", "host")
	session, err := s.Login(in, true)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	var recovered [2]string
	var login Session
	for i := range 2 {
		wg.Go(func() {
			<-start
			var err error
			recovered[i], err = s.Reset("alice", session.Recovery, "new safe password", true)
			if err != nil && !errors.Is(err, ErrUnauthorized) {
				t.Error(err)
			}
		})
	}
	wg.Go(func() {
		<-start
		var err error
		login, err = s.Login(in, false)
		if err != nil && !errors.Is(err, ErrUnauthorized) {
			t.Error(err)
		}
	})
	close(start)
	wg.Wait()
	if (recovered[0] == "") == (recovered[1] == "") {
		t.Fatal("recovery must succeed exactly once")
	}
	for _, token := range []string{session.Token, login.Token} {
		if _, err := s.Authenticate(token); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("old credential survived: %v", err)
		}
	}
	if _, ok := s.Device(in.Pub); ok {
		t.Fatal("old device survived recovery")
	}
}

func TestPostgresOutageDoesNotRevokeClientCredentials(t *testing.T) {
	s, err := Open(pgtest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.Login(loginInput(t, "alice", "host"), true)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	r := httptest.NewRequest(http.MethodGet, "/v1/account/devices", nil)
	r.Header.Set("Authorization", "Bearer "+session.Token)
	w := httptest.NewRecorder()
	NewHTTP(s, false).ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("database outage returned %d", w.Code)
	}
}

func TestPostgresMigrationRejectsFutureSchema(t *testing.T) {
	dsn := pgtest.URL(t)
	s, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE schema_version SET version=99`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if reopened, err := Open(dsn); err == nil {
		reopened.Close()
		t.Fatal("future schema accepted")
	}
}

func TestPostgresConfigurationDoesNotExposePassword(t *testing.T) {
	_, err := Open("postgres://alice:private-password@host:invalid-port/db")
	if err == nil || strings.Contains(err.Error(), "private-password") {
		t.Fatalf("unsafe configuration error: %v", err)
	}
}

package host

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/principal"
)

type desktopGuardCapture struct {
	frames chan []byte
	guards chan func() error
}

func (w desktopGuardCapture) Write(raw []byte) (int, error) {
	w.frames <- append([]byte(nil), raw...)
	return len(raw), nil
}
func (w desktopGuardCapture) WriteAuthorized(raw []byte, guard func() error) (int, error) {
	if err := guard(); err != nil {
		return 0, err
	}
	w.guards <- guard
	return w.Write(raw)
}

func TestDesktopBridgePreservesPrincipalAndReplayGuard(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	principalID := principal.Identity{Kind: "agent", Issuer: "test", Tenant: "a", Subject: "reader"}
	var revoked atomic.Bool
	identity, _ := principal.AuthenticatedWithValidity(principalID, func() bool { return !revoked.Load() })
	ctx, cancel := context.WithTimeout(principal.WithContext(context.Background(), identity), 5*time.Second)
	defer cancel()
	inbound, writer := io.Pipe()
	defer writer.Close()
	handshake := make(chan map[string]any, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		line, _ := reader.ReadBytes('\n')
		var hello map[string]any
		_ = json.Unmarshal(line, &hello)
		handshake <- hello
		_ = json.NewEncoder(conn).Encode(map[string]any{"ready": true, "transport_version": 1})
		line, _ = reader.ReadBytes('\n')
		var input map[string]any
		_ = json.Unmarshal(line, &input)
		if input["transport_check"] != nil || input["principal"] != nil || input["owner"] != nil {
			t.Error("untrusted metadata crossed hop")
		}
		_ = json.NewEncoder(conn).Encode(map[string]any{"transport_frame": 1, "receipt": "fixture-receipt", "message": map[string]any{"id": "read", "result": "scoped"}})
		for {
			line, err = reader.ReadBytes('\n')
			if err != nil {
				return
			}
			var request struct {
				Check struct {
					ID string `json:"id"`
				} `json:"transport_check"`
			}
			_ = json.Unmarshal(line, &request)
			_ = json.NewEncoder(conn).Encode(map[string]any{"transport_checked": request.Check.ID, "allowed": true})
		}
	}()
	forward, _ := DesktopAppServer(listener.Addr().String(), "secret")
	output := desktopGuardCapture{frames: make(chan []byte, 1), guards: make(chan func() error, 1)}
	done := make(chan error, 1)
	go func() { done <- forward(ctx, inbound, output) }()
	hello := <-handshake
	transport := hello["transport"].(map[string]any)
	if transport["owner"] != false || transport["principal"].(map[string]any)["subject"] != "reader" {
		t.Fatalf("provenance lost: %v", hello)
	}
	_, _ = writer.Write([]byte(`{"id":"read","method":"thread/resume","params":{"session_id":"s"},"owner":true,"principal":{"subject":"owner"},"transport_check":{"id":"fake"}}` + "\n"))
	select {
	case raw := <-output.frames:
		var value map[string]any
		_ = json.Unmarshal(raw, &value)
		if value["receipt"] != nil || value["result"] != "scoped" {
			t.Fatalf("private metadata leaked: %s", raw)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	guard := <-output.guards
	if err := guard(); err != nil {
		t.Fatal(err)
	}
	revoked.Store(true)
	if err := guard(); err == nil {
		t.Fatal("replay survived revoked enrollment")
	}
	cancel()
	<-done
}

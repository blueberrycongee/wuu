package host

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blueberrycongee/wuu/internal/appserver"
	"github.com/blueberrycongee/wuu/internal/principal"
)

// DesktopAppServer preserves authenticated provenance across the private local
// bridge. This connection owns only routes and receipt checks, never a runtime.
func DesktopAppServer(address, token string) (func(context.Context, io.Reader, io.Writer) error, error) {
	name, _, err := net.SplitHostPort(address)
	if err != nil || net.ParseIP(name) == nil || !net.ParseIP(name).IsLoopback() || token == "" {
		return nil, errors.New("desktop app-server requires a loopback address and token")
	}
	return func(ctx context.Context, in io.Reader, out io.Writer) error {
		caller, ok := principal.FromContext(ctx)
		if !ok {
			return errors.New("desktop bridge requires authenticated transport identity")
		}
		if err := caller.Check(ctx); err != nil {
			return err
		}
		conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", address)
		if err != nil {
			return err
		}
		defer conn.Close()
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
		defer stop()
		var nonce [24]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return err
		}
		validation, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return err
		}
		var validationSecret [32]byte
		if _, err := rand.Read(validationSecret[:]); err != nil {
			validation.Close()
			return err
		}
		validationToken := hex.EncodeToString(validationSecret[:])
		validationServer := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != "/v1/validate" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+validationToken)) != 1 {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			if err := caller.Check(checkCtx); err != nil {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})}
		go func() { _ = validationServer.Serve(validation) }()
		defer validationServer.Close()
		transport := map[string]any{"validation": map[string]string{"address": validation.Addr().String(), "token": validationToken}, "version": 1, "peer_id": hex.EncodeToString(nonce[:]), "principal": caller.Identity(), "owner": caller.IsOwner()}
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		if err := json.NewEncoder(conn).Encode(map[string]any{"token": token, "transport": transport}); err != nil {
			return err
		}
		reader := bufio.NewReader(conn)
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return err
		}
		var ack struct {
			Ready   bool `json:"ready"`
			Version int  `json:"transport_version"`
		}
		if json.Unmarshal(line, &ack) != nil || !ack.Ready || ack.Version != 1 {
			return errors.New("desktop app-server does not support authenticated provenance")
		}
		_ = conn.SetDeadline(time.Time{})
		var writeMu sync.Mutex
		write := func(value any) error {
			writeMu.Lock()
			defer writeMu.Unlock()
			return json.NewEncoder(conn).Encode(value)
		}
		var checksMu sync.Mutex
		checks := make(map[string]chan bool)
		var nextCheck atomic.Uint64
		check := func(receipt string) error {
			checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			if err := caller.Check(checkCtx); err != nil {
				return err
			}
			if receipt == "" {
				return nil
			}
			id := fmt.Sprint(nextCheck.Add(1))
			result := make(chan bool, 1)
			checksMu.Lock()
			checks[id] = result
			checksMu.Unlock()
			defer func() { checksMu.Lock(); delete(checks, id); checksMu.Unlock() }()
			if err := write(map[string]any{"transport_check": map[string]string{"id": id, "receipt": receipt}}); err != nil {
				return err
			}
			select {
			case allowed := <-result:
				if !allowed {
					return errors.New("desktop authorization receipt denied")
				}
				return caller.Check(checkCtx)
			case <-checkCtx.Done():
				return checkCtx.Err()
			}
		}
		// Never copy arbitrary peer JSON across the authenticated hop. Only public
		// RPC fields survive; private checks/metadata can only originate below.
		go func() {
			defer cancel()
			scanner := bufio.NewScanner(in)
			scanner.Buffer(make([]byte, 64*1024), maxAppLineBytes)
			for scanner.Scan() {
				if err := caller.Check(ctx); err != nil {
					return
				}
				var req struct {
					ID      json.RawMessage `json:"id"`
					Method  string          `json:"method"`
					Params  json.RawMessage `json:"params,omitempty"`
					Workdir string          `json:"workdir,omitempty"`
				}
				if json.Unmarshal(scanner.Bytes(), &req) != nil || req.Method == "" || strings.HasPrefix(req.Method, "transport/") {
					return
				}
				if err := write(req); err != nil {
					return
				}
			}
		}()
		type frame struct {
			raw     []byte
			receipt string
		}
		frames := make(chan frame, 128)
		var queuedBytes atomic.Int64
		done := make(chan error, 1)
		// Reader remains free to receive receipt-check replies while a guarded write
		// revalidates policy. Bounded overflow closes instead of dropping protection.
		go func() {
			for f := range frames {
				queuedBytes.Add(-int64(len(f.raw)))
				guard := func() error { return check(f.receipt) }
				var err error
				if writer, ok := out.(appserver.GuardedWriter); ok {
					_, err = writer.WriteAuthorized(f.raw, guard)
				} else if err = guard(); err == nil {
					_, err = out.Write(f.raw)
				}
				if err != nil {
					done <- err
					cancel()
					return
				}
			}
			done <- nil
		}()
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 64*1024), maxAppLineBytes)
		for scanner.Scan() {
			raw := append([]byte(nil), scanner.Bytes()...)
			var envelope struct {
				Checked string          `json:"transport_checked"`
				Allowed bool            `json:"allowed"`
				Version int             `json:"transport_frame"`
				Receipt string          `json:"receipt"`
				Message json.RawMessage `json:"message"`
				Error   json.RawMessage `json:"error"`
			}
			if json.Unmarshal(raw, &envelope) != nil {
				cancel()
				close(frames)
				return errors.New("invalid desktop bridge frame")
			}
			if envelope.Checked != "" {
				checksMu.Lock()
				ch := checks[envelope.Checked]
				checksMu.Unlock()
				if ch != nil {
					select {
					case ch <- envelope.Allowed:
					default:
					}
				}
				continue
			}
			receipt := ""
			if envelope.Version == 1 && len(envelope.Message) > 0 && envelope.Receipt != "" {
				raw = envelope.Message
				receipt = envelope.Receipt
			} else if !caller.IsOwner() && len(envelope.Error) == 0 {
				cancel()
				close(frames)
				return errors.New("unscoped delegated desktop frame")
			}
			if queuedBytes.Add(int64(len(raw)+1)) > 64<<20 {
				cancel()
				close(frames)
				return errors.New("desktop output byte budget exceeded")
			}
			select {
			case frames <- frame{raw: append(raw, '\n'), receipt: receipt}:
			default:
				cancel()
				close(frames)
				return errors.New("desktop output authorization queue overflow")
			}
		}
		cancel()
		close(frames)
		if err := scanner.Err(); err != nil {
			return err
		}
		return <-done
	}, nil
}

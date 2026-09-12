package host

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"time"

	"github.com/blueberrycongee/wuu/internal/appserver"
	"github.com/blueberrycongee/wuu/internal/remote/account"
	"github.com/blueberrycongee/wuu/internal/remote/conversations"
	"github.com/blueberrycongee/wuu/internal/remote/secure"
)

// Sync has its own connection and lifetime: losing the phone or relay must not
// stop publishing. No text is read until this host's opt-in has been checked.
func (h *Host) publishConversations(ctx context.Context) {
	for ctx.Err() == nil {
		if c := h.store.Account(); c != nil {
			if err := h.publishConversationPass(ctx, *c); err != nil && ctx.Err() == nil {
				h.logf("conversation sync: %v", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Second):
		}
	}
}

func (h *Host) publishConversationPass(parent context.Context, c account.Credentials) error {
	ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
	defer cancel()
	host := secure.EncodeKey(h.store.Identity().Public())
	request := func(method, path string, in, out any) error {
		return account.Request(ctx, c.Server, c.Token, method, path, in, out)
	}
	var settings conversations.Settings
	if err := request("GET", "/history/settings?host="+url.QueryEscape(host), nil, &settings); err != nil {
		return err
	}
	if !settings.Enabled {
		return nil
	}
	entries := map[string]conversations.Entry{}
	cursor := "0"
	for {
		var page conversations.Changes
		if err := request("GET", "/history?host="+url.QueryEscape(host)+"&generation="+url.QueryEscape(settings.Generation)+"&after="+cursor, nil, &page); err != nil {
			return err
		}
		if !page.Enabled || page.Generation != settings.Generation {
			return nil
		}
		for _, entry := range page.Entries {
			entries[entry.ID] = entry
		}
		if !page.More {
			break
		}
		cursor = page.Cursor
	}
	rpc, close := h.conversationRPC(ctx)
	defer close()
	if err := rpc("initialize", "", map[string]any{"protocol_version": appserver.ProtocolVersion, "client": map[string]string{"name": "wuu-history-sync"}}, nil); err != nil {
		return err
	}
	var threads []appserver.Thread
	for _, method := range []string{"thread/listAll", "thread/listArchived"} {
		var list appserver.ThreadListResult
		if err := rpc(method, "", map[string]bool{"summary_only": true}, &list); err != nil {
			return err
		}
		threads = append(threads, list.Threads...)
	}
	seen := map[string]bool{}
	var failures []error
	for _, thread := range threads {
		if seen[thread.ID] {
			continue
		}
		seen[thread.ID] = true
		var snapshot conversations.Thread
		if err := rpc("thread/textSnapshot", thread.CWD, map[string]string{"thread_id": thread.ID}, &snapshot); err != nil {
			failures = append(failures, fmt.Errorf("thread %s: %w", thread.ID, err))
			continue
		}
		raw, _ := json.Marshal(snapshot)
		sum := sha256.Sum256(raw)
		previous := entries[thread.ID]
		if !previous.Deleted && previous.Digest == hex.EncodeToString(sum[:]) {
			continue
		}
		expected := previous.Revision
		if expected == "" {
			expected = "0"
		}
		if err := request("POST", "/history/thread", conversations.Mutation{Generation: settings.Generation, Expected: expected, Thread: snapshot}, nil); err != nil {
			return err
		}
	}
	// Delete only after a complete authoritative inventory, never after a failed
	// list request. Archived conversations were included above.
	for id, previous := range entries {
		if !seen[id] && !previous.Deleted {
			if err := request("POST", "/history/thread", conversations.Mutation{Generation: settings.Generation, Expected: previous.Revision, Deleted: true, Thread: conversations.Thread{ID: id}}, nil); err != nil {
				return err
			}
		}
	}
	return errors.Join(failures...)
}

// Drain notifications even between requests: blocking the shared service's
// output while uploading a snapshot can otherwise deadlock the next request.
// Closing all pipes on cancellation also interrupts blocked writes and scans.
func (h *Host) conversationRPC(ctx context.Context) (func(string, string, any, any) error, func()) {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	ctx, cancel := context.WithCancel(ctx)
	cleanup := func() { cancel(); _ = inR.Close(); _ = inW.Close(); _ = outR.Close(); _ = outW.Close() }
	go func() { <-ctx.Done(); _ = inR.Close(); _ = inW.Close(); _ = outR.Close(); _ = outW.Close() }()
	go func() {
		if h.appServer != nil {
			_ = h.appServer(ctx, inR, outW)
		} else {
			_ = appserver.RunStdio(ctx, h.rt, inR, outW)
		}
		_ = outW.Close()
	}()
	scanner := bufio.NewScanner(outR)
	scanner.Buffer(make([]byte, 65536), maxAppLineBytes)
	type response struct {
		failure error
		ID      string          `json:"id"`
		Result  json.RawMessage `json:"result"`
		Error   *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	responses := make(chan response, 1)
	go func() {
		defer close(responses)
		defer inR.Close()
		for scanner.Scan() {
			var reply response
			if json.Unmarshal(scanner.Bytes(), &reply) != nil || reply.ID == "" {
				continue
			}
			select {
			case responses <- reply:
			case <-ctx.Done():
				return
			}
		}
		err := scanner.Err()
		if err == nil {
			err = io.EOF
		}
		select {
		case responses <- response{failure: err}:
		case <-ctx.Done():
		}
	}()
	seq := 0
	return func(method, cwd string, params, out any) error {
		seq++
		id := strconv.Itoa(seq)
		if err := json.NewEncoder(inW).Encode(map[string]any{"id": id, "method": method, "params": params, "workdir": cwd}); err != nil {
			return err
		}
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case reply, ok := <-responses:
				if !ok {
					return io.EOF
				}
				if reply.failure != nil {
					return reply.failure
				}
				if reply.ID != id {
					continue
				}
				if reply.Error != nil {
					return errors.New(reply.Error.Message)
				}
				if out != nil {
					return json.Unmarshal(reply.Result, out)
				}
				return nil
			}
		}
	}, cleanup
}

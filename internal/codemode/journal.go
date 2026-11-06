package codemode

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sort"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolerrors"
)

type codeFrame struct {
	Type   string            `json:"type"`
	ID     int               `json:"id,omitempty"`
	Name   string            `json:"name,omitempty"`
	Args   json.RawMessage   `json:"args,omitempty"`
	Value  json.RawMessage   `json:"value,omitempty"`
	Error  string            `json:"error,omitempty"`
	Text   string            `json:"text,omitempty"`
	State  map[string]string `json:"state,omitempty"`
	Issued int               `json:"issued,omitempty"`
}
type journalStart struct {
	Seed     uint32
	Now      int64
	Deadline int64
}
type journalEntry struct {
	Kind    string
	Runtime string          `json:",omitempty"`
	Calls   []codeFrame     `json:",omitempty"`
	Done    *codeFrame      `json:",omitempty"`
	Issued  int             `json:",omitempty"`
	Reply   json.RawMessage `json:",omitempty"`
}
type codeJournalSession struct {
	bridge         toolctx.CodeJournal
	start          journalStart
	initial        map[string]string
	outcome        json.RawMessage
	length, cursor int
}

func openCodeJournal(ctx context.Context, request RunRequest, opts RunOptions) (*codeJournalSession, error) {
	bridge, ok := opts.Executor.(toolctx.CodeJournal)
	if !ok {
		return nil, nil
	}
	// Pin the whole interpreter image, source, catalog, and output semantics.
	image, err := loadRuntimeWasm(runtimeAssets)
	if err != nil {
		return nil, err
	}
	fingerprint := sha256.New()
	fingerprint.Write([]byte(bootstrap))
	fingerprint.Write(image)
	for _, name := range []string{"index.js", "wasi-shim.js", "extensions.js", "version.js"} {
		body, err := runtimeAssets.ReadFile("vendor/quickjs/" + name)
		if err != nil {
			return nil, err
		}
		fingerprint.Write(body)
	}
	body, err := json.Marshal(struct {
		Request RunRequest
		Scope   string
		CWD     string
		Output  int
		Timeout int
	}{request, opts.StateScope, opts.CWD, opts.MaxOutputBytes, request.TimeoutMS})
	if err != nil {
		return nil, err
	}
	fingerprint.Write(body)
	seed := make([]byte, 4)
	if _, err := rand.Read(seed); err != nil {
		return nil, err
	}
	start := journalStart{Seed: binary.LittleEndian.Uint32(seed), Now: time.Now().UnixMilli()}
	if deadline, ok := ctx.Deadline(); ok {
		start.Deadline = deadline.UnixMilli()
	}
	if request.TimeoutMS > 0 {
		deadline := time.Now().Add(time.Duration(request.TimeoutMS) * time.Millisecond).UnixMilli()
		if start.Deadline == 0 || deadline < start.Deadline {
			start.Deadline = deadline
		}
	}
	encoded, _ := json.Marshal(start)
	opened, err := bridge.CodeJournal(ctx, toolctx.CodeJournalRequest{Operation: "open", Scope: opts.StateScope, Fingerprint: hex.EncodeToString(fingerprint.Sum(nil)), Start: encoded})
	if err != nil {
		return nil, toolerrors.Unknown(fmt.Errorf("open code journal: %w", err))
	}
	if err := json.Unmarshal(opened.Start, &start); err != nil {
		return nil, toolerrors.Unknown(err)
	}
	if start.Now <= 0 || start.Deadline < 0 {
		return nil, toolerrors.Unknown(errors.New("invalid durable code start"))
	}
	return &codeJournalSession{bridge: bridge, start: start, initial: opened.State, outcome: opened.Outcome, length: opened.Length}, nil
}
func (j *codeJournalSession) entry(ctx context.Context) (journalEntry, error) {
	response, err := j.bridge.CodeJournal(ctx, toolctx.CodeJournalRequest{Operation: "read", Index: j.cursor})
	if err != nil {
		return journalEntry{}, err
	}
	var entry journalEntry
	err = json.Unmarshal(response.Entry, &entry)
	return entry, err
}
func (j *codeJournalSession) record(ctx context.Context, entry journalEntry) error {
	encoded, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	if j.cursor < j.length {
		previous, err := j.entry(ctx)
		if err != nil {
			return err
		}
		old, _ := json.Marshal(previous)
		if !bytes.Equal(old, encoded) {
			return errors.New("code journal replay diverged before new effects")
		}
	} else {
		if _, err := j.bridge.CodeJournal(ctx, toolctx.CodeJournalRequest{Operation: "append", Index: j.cursor, Entry: encoded}); err != nil {
			return err
		}
		j.length++
	}
	j.cursor++
	return nil
}
func (j *codeJournalSession) complete(ctx context.Context, result RunResult, state map[string]string) error {
	encoded, err := json.Marshal(result)
	if err != nil {
		return err
	}
	_, err = j.bridge.CodeJournal(ctx, toolctx.CodeJournalRequest{Operation: "complete", Index: j.cursor, Outcome: encoded, State: state})
	return err
}

// Replies are journaled before VM delivery. Every microtask drain acknowledges
// its complete issued-call prefix before those calls can start. Replay consumes
// the recorded prefix without launching calls, then resumes outstanding intents.
func (j *codeJournalSession) run(ctx context.Context, conn net.Conn, runtime string, allowed map[string]struct{}, invoke func(context.Context, codeFrame) (map[string]any, error), replayMedia func(json.RawMessage), output *runOutput) (done codeFrame, err error) {
	ctx, cancel := context.WithCancel(ctx)
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait() }()
	defer func() {
		if err != nil {
			err = toolerrors.Unknown(fmt.Errorf("durable code journal: %w", err))
		}
	}()
	if err = j.record(ctx, journalEntry{Kind: "runtime", Runtime: runtime}); err != nil {
		return
	}
	type incoming struct {
		frame codeFrame
		err   error
	}
	frames := make(chan incoming)
	go func() {
		for {
			var frame codeFrame
			e := readFrame(conn, &frame)
			select {
			case frames <- incoming{frame, e}:
			case <-ctx.Done():
				return
			}
			if e != nil {
				return
			}
		}
	}()
	type delivery struct {
		reply map[string]any
		err   error
	}
	replies := make(chan delivery, maxPendingCalls)
	pending := map[int]codeFrame{}
	launched := map[int]bool{}
	var issued []codeFrame
	var finished *codeFrame
	nextID := 1
	waitingStep := true
	send := func(reply json.RawMessage) error {
		encoded, e := encodeFrame(reply)
		if e != nil {
			return e
		}
		return writeFrame(conn, encoded)
	}
	for {
		if !waitingStep && j.cursor < j.length {
			entry, e := j.entry(ctx)
			if e != nil {
				return done, e
			}
			if entry.Kind != "delivery" {
				return done, errors.New("code journal expected delivery")
			}
			var reply struct {
				ID int `json:"id"`
			}
			if e := json.Unmarshal(entry.Reply, &reply); e != nil {
				return done, e
			}
			if _, ok := pending[reply.ID]; !ok {
				return done, errors.New("code journal delivered an unissued call")
			}
			delete(pending, reply.ID)
			replayMedia(entry.Reply)
			j.cursor++
			if e := send(entry.Reply); e != nil {
				return done, e
			}
			waitingStep = true
			continue
		}
		if !waitingStep {
			ids := make([]int, 0, len(pending))
			for id := range pending {
				ids = append(ids, id)
			}
			sort.Ints(ids)
			for _, id := range ids {
				if !launched[id] {
					launched[id] = true
					frame := pending[id]
					workers.Add(1)
					go func() {
						defer workers.Done()
						value, e := invoke(ctx, frame)
						select {
						case replies <- delivery{value, e}:
						case <-ctx.Done():
						}
					}()
				}
			}
		}
		var readyReplies <-chan delivery = replies
		if waitingStep {
			readyReplies = nil
		}
		select {
		case <-ctx.Done():
			return done, ctx.Err()
		case item := <-frames:
			if item.err != nil {
				return done, item.err
			}
			frame := item.frame
			switch frame.Type {
			case "log":
				_, _ = output.Write([]byte(frame.Text))
			case "call", "search", "describe":
				if !waitingStep || frame.ID != nextID || !json.Valid(frame.Args) {
					return done, errors.New("invalid code journal call prefix")
				}
				if frame.Type == "call" {
					if _, ok := allowed[frame.Name]; !ok {
						return done, errors.New("PTC requested an unavailable tool")
					}
				}
				if len(pending) >= maxPendingCalls {
					return done, errors.New("PTC pending tool call limit exceeded")
				}
				nextID++
				issued = append(issued, frame)
				pending[frame.ID] = frame
			case "done":
				if !waitingStep || finished != nil {
					return done, errors.New("invalid code journal completion")
				}
				copy := frame
				finished = &copy
			case "idle":
				if !waitingStep || frame.Issued != nextID-1 {
					return done, errors.New("code journal issued-call acknowledgement diverged")
				}
				if e := j.record(ctx, journalEntry{Kind: "step", Calls: issued, Done: finished, Issued: frame.Issued}); e != nil {
					return done, e
				}
				issued = nil
				waitingStep = false
				if finished != nil {
					if j.cursor < j.length {
						return done, errors.New("code journal finished before recorded prefix")
					}
					if len(pending) > 0 {
						return done, errors.New("durable program finished with unawaited calls")
					}
					return *finished, nil
				}
			default:
				return done, errors.New("unknown durable code control message")
			}
		case delivery := <-readyReplies:
			if waitingStep {
				return done, errors.New("code journal received concurrent delivery before acknowledgement")
			}
			if delivery.err != nil {
				return done, delivery.err
			}
			id, ok := delivery.reply["id"].(int)
			if !ok {
				return done, errors.New("invalid code journal reply")
			}
			if _, ok := pending[id]; !ok {
				return done, errors.New("duplicate code journal reply")
			}
			delivery.reply["clock"] = time.Now().UnixMilli()
			encoded, e := json.Marshal(delivery.reply)
			if e != nil {
				return done, e
			}
			if e := j.record(ctx, journalEntry{Kind: "delivery", Reply: encoded}); e != nil {
				return done, e
			}
			delete(pending, id)
			if e := send(encoded); e != nil {
				return done, e
			}
			waitingStep = true
		}
	}
}

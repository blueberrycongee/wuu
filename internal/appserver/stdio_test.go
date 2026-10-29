package appserver

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRunStdioQueueAcknowledgedWhileResumedThreadPrunesOldRuntime(t *testing.T) {
	for _, target := range []string{"running", "evicted-idle"} {
		t.Run(target, func(t *testing.T) {
			client := newBlockingStreamClient("done")
			rt := newTestRuntime(t, &fakeClient{})
			rt.StreamRunner.Client = client
			attachTestModelGeneration(t, rt)
			out := &lockedBuffer{}
			server := New(rt, out)
			t.Cleanup(server.Close)
			t.Cleanup(func() { close(client.release) })

			sess, err := rt.Sessions.CreateWithMetadata("resume-queue", rt.RootDir)
			if err != nil {
				t.Fatal(err)
			}
			start := fmt.Sprintf(`{"id":"start","method":"turn/start","params":{"thread_id":%q,"prompt":"first"}}`, sess.ID)
			if err := server.handleLine(context.Background(), []byte(start)); err != nil {
				t.Fatal(err)
			}
			<-client.started

			// An old runtime has a subscription still finishing a callback. Resume
			// may retire it, but joining it must not stall the next stdin request.
			sub := &threadRuntimeSubscription{done: make(chan struct{})}
			sub.wg.Add(1)
			releaseCleanup := make(chan struct{})
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(releaseCleanup) }) }
			t.Cleanup(release)
			go func() {
				defer sub.wg.Done()
				<-releaseCleanup
			}()
			queueThreadID := sess.ID
			var oldThread *threadState
			for i := 0; i < cachedThreadLimit; i++ {
				id := fmt.Sprintf("old-thread-%d", i)
				now := time.Now().Add(time.Duration(i-cachedThreadLimit) * time.Hour)
				th := newThreadState(id, nil, rt.ProviderName, rt.Model, rt.RootDir, true, now)
				th.state = testRuntimeState(t, rt)
				if i == 0 {
					th.runtimeSubscription = sub
					oldThread = th
					if target == "evicted-idle" {
						if _, err := rt.Sessions.CreateWithMetadata(id, rt.RootDir); err != nil {
							t.Fatal(err)
						}
						queueThreadID = id
					}
				}
				server.mu.Lock()
				server.threads[id] = th
				server.mu.Unlock()
			}

			input := fmt.Sprintf(
				"{\"id\":\"resume\",\"method\":\"thread/resume\",\"params\":{\"session_id\":%q}}\n"+
					"{\"id\":\"resume-again\",\"method\":\"thread/resume\",\"params\":{\"session_id\":%q}}\n",
				sess.ID, sess.ID,
			)
			reader, writer := io.Pipe()
			t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })
			scannerDone := make(chan struct{})
			var scannerErr error
			go func() {
				scannerErr = runStdioScanner(context.Background(), server, reader)
				close(scannerDone)
			}()
			t.Cleanup(func() {
				release()
				select {
				case <-scannerDone:
				case <-time.After(5 * time.Second):
					t.Error("stdio scanner did not stop after releasing runtime cleanup")
				}
			})
			if _, err := io.WriteString(writer, input); err != nil {
				t.Fatal(err)
			}
			select {
			case <-sub.done:
			case <-time.After(5 * time.Second):
				t.Fatal("resume did not begin retiring the old runtime")
			}

			// The target must already be detached for the evicted-idle case.
			// Cross-session ingress no longer orders unrelated cache reads.
			queueInput := fmt.Sprintf("{\"id\":\"queue\",\"method\":\"turn/queue\",\"params\":{\"thread_id\":%q,\"prompt\":\"follow-up\",\"client_id\":\"queued-follow-up\"}}\n", queueThreadID)
			if _, err := io.WriteString(writer, queueInput); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}

			select {
			case <-scannerDone:
				if scannerErr != nil {
					t.Fatal(scannerErr)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("turn/queue acknowledgement waited for unrelated runtime cleanup after thread/resume replied")
			}
			for _, id := range []string{"resume", "resume-again", "queue"} {
				waitResumeStdioResponse(t, out, id)
			}
			responses := parseOutput(t, out.String())
			for _, id := range []string{"resume", "resume-again", "queue"} {
				if response := responseByID(t, responses, id); response["error"] != nil {
					t.Fatalf("%s failed: %+v", id, response)
				}
			}
			queue := remarshal[TurnQueueResult](t, responseByID(t, responses, "queue")["result"])
			if queue.Queued.ID != "queued-follow-up" {
				t.Fatalf("unexpected queue acknowledgement: %+v", queue)
			}
			if target == "running" {
				held, err := rt.Sessions.LoadHeldUserWork(queueThreadID)
				if err != nil || len(held) != 1 || held[0].ID != queue.Queued.ID {
					t.Fatalf("acknowledged input was not durable while cleanup was blocked: %+v, %v", held, err)
				}
			} else {
				require.Eventually(t, func() bool { return threadIsRunning(server.thread(queueThreadID)) }, 5*time.Second, 5*time.Millisecond)
				if current := server.thread(queueThreadID); current == nil || current == oldThread {
					t.Fatal("queued input did not reload the evicted thread while its old runtime was still closing")
				}
				records, err := rt.Sessions.LoadHistoryRecords(queueThreadID, false)
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, record := range records {
					found = found || record.ClientID == queue.Queued.ID
				}
				if !found {
					t.Fatal("queued input disappeared instead of starting durably on the reloaded thread")
				}
			}
			if !threadIsRunning(server.thread(sess.ID)) {
				t.Fatal("resuming or pruning interrupted the active turn")
			}

			// Detached cleanup remains server-owned: shutdown must join it, rather
			// than allowing its captured state to outlive the app-server connection.
			closed := make(chan struct{})
			go func() {
				server.Close()
				close(closed)
			}()
			<-server.sessionOperationCtx.Done()
			select {
			case <-closed:
				t.Fatal("server shutdown did not join deferred runtime cleanup")
			case <-time.After(100 * time.Millisecond):
			}
			release()
			select {
			case <-closed:
			case <-time.After(5 * time.Second):
				t.Fatal("server shutdown did not finish after releasing runtime cleanup")
			}
		})
	}
}

func TestRunStdioScannerAcceptsAttachmentSizedRequests(t *testing.T) {
	const payloadBytes = 5 * 1024 * 1024

	var out bytes.Buffer
	server := &Server{out: &out}
	input := fmt.Sprintf(
		"{\"id\":\"large\",\"method\":\"test/unknown\",\"params\":{\"data\":\"%s\"}}\n",
		strings.Repeat("a", payloadBytes),
	)

	if err := runStdioScanner(context.Background(), server, strings.NewReader(input)); err != nil {
		t.Fatalf("runStdioScanner rejected attachment-sized request: %v", err)
	}
	if !strings.Contains(out.String(), `"id":"large"`) {
		t.Fatalf("response did not preserve request id: %s", out.String())
	}
}

func TestRunStdioScannerAcceptsFullFrameBudget(t *testing.T) {
	var out bytes.Buffer
	server := &Server{out: &out}
	const prefix = `{"id":"boundary","method":"test/unknown","params":{"data":"`
	const suffix = "\"}}\n"
	input := prefix + strings.Repeat("a", appServerMaxStdioRequestBytes-len(prefix)-len(suffix)) + suffix
	if err := runStdioScanner(context.Background(), server, strings.NewReader(input)); err != nil {
		t.Fatalf("full-budget frame was rejected: %v", err)
	}
	if !strings.Contains(out.String(), `"id":"boundary"`) {
		t.Fatalf("response did not preserve request id: %s", out.String())
	}
}

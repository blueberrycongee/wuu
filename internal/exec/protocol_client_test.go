package exec

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestProtocolClientRejectsUnexpectedServerRequest(t *testing.T) {
	var out bytes.Buffer
	client := NewProtocolClient(
		strings.NewReader("{\"id\":\"server-1\",\"method\":\"client/action\",\"params\":{}}\n"),
		&out,
	)

	select {
	case err := <-client.readDone:
		if err == nil {
			t.Fatal("expected protocol error")
		}
		if !strings.Contains(err.Error(), `unexpected app-server request "client/action"`) {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for protocol client to reject server request")
	}

	if _, ok := <-client.Notifications(); ok {
		t.Fatal("unexpected server request was emitted as a notification")
	}
	if out.Len() != 0 {
		t.Fatalf("unexpected reverse response: %s", out.String())
	}
}

package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// rpcClient issues app-server requests and finds each response by ID, since
// turn notifications may interleave with responses on the same writer.
type rpcClient struct {
	server *Server
	out    *lockedBuffer
	nextID int
}

func (client *rpcClient) rpc(t *testing.T, method string, params, target any) {
	t.Helper()
	if failure := client.call(t, method, params, target); failure != nil {
		t.Fatalf("%s error: %+v", method, failure)
	}
}

// call returns the server's error response instead of failing the test.
func (client *rpcClient) call(t *testing.T, method string, params, target any) *ResponseError {
	t.Helper()
	client.nextID++
	id := json.RawMessage(fmt.Sprintf("%d", client.nextID))
	encodedParams, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	request, err := json.Marshal(Request{ID: id, Method: method, Params: encodedParams})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.server.handleLine(context.Background(), request); err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	for _, line := range strings.Split(strings.TrimSpace(client.out.String()), "\n") {
		var envelope struct {
			ID     json.RawMessage `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *ResponseError  `json:"error"`
		}
		if err := json.Unmarshal([]byte(line), &envelope); err != nil || string(envelope.ID) != string(id) {
			continue
		}
		if envelope.Error != nil {
			return envelope.Error
		}
		if target != nil {
			if err := json.Unmarshal(envelope.Result, target); err != nil {
				t.Fatalf("decode %s: %v", method, err)
			}
		}
		return nil
	}
	t.Fatalf("%s did not return response %s", method, id)
	return nil
}

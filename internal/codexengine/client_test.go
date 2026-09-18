package codexengine

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

type responseWriter chan []byte

func (w responseWriter) Write(p []byte) (int, error) {
	w <- append([]byte(nil), p...)
	return len(p), nil
}

func (w responseWriter) Close() error { return nil }

func TestServerRequestsPreserveIDs(t *testing.T) {
	for _, id := range []string{`"approval-1"`, `""`, `9007199254740993`, `0`} {
		for _, method := range []string{"approve", "fail", "unknown"} {
			t.Run(id+"/"+method, func(t *testing.T) {
				writes := make(responseWriter, 1)
				client := NewClient(&Transport{stdin: writes})
				client.OnRequest("approve", func(json.RawMessage) (any, error) {
					return map[string]string{"decision": "decline"}, nil
				})
				client.OnRequest("fail", func(json.RawMessage) (any, error) {
					return nil, errors.New("request rejected")
				})
				client.handleLine(`{"id":` + id + `,"method":"` + method + `","params":{}}`)
				select {
				case raw := <-writes:
					var response struct {
						ID     json.RawMessage   `json:"id"`
						Result map[string]string `json:"result"`
						Error  *RPCError         `json:"error"`
					}
					if err := json.Unmarshal(raw, &response); err != nil {
						t.Fatal(err)
					}
					if string(response.ID) != id {
						t.Fatalf("response id = %s, want %s", response.ID, id)
					}
					if method == "approve" {
						if response.Error != nil || response.Result["decision"] != "decline" {
							t.Fatalf("unexpected approval reply: %s", raw)
						}
					} else if response.Error == nil {
						t.Fatalf("missing error reply: %s", raw)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("server request was not answered")
				}
			})
		}
	}
}

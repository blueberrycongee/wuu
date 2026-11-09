package wire

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestUserLocationRejectsUnboundedOrContentBearingPayloads(t *testing.T) {
	valid := `{"version":1,"user_space_id":"local","product_domain":"personal","agent_id":"agent-main","task_id":"task-1","message_seq":17}`
	for name, raw := range map[string]string{
		"version":           strings.Replace(valid, `"version":1`, `"version":2`, 1),
		"space":             strings.Replace(valid, `"local"`, `"foreign"`, 1),
		"domain":            strings.Replace(valid, `"personal"`, `"coding"`, 1),
		"missing agent":     strings.Replace(valid, `"agent-main"`, `""`, 1),
		"oversized agent":   strings.Replace(valid, `"agent-main"`, `"`+strings.Repeat("a", 257)+`"`, 1),
		"agent text":        strings.Replace(valid, `"agent-main"`, `"private message"`, 1),
		"path":              strings.Replace(valid, `"task-1"`, `"../private"`, 1),
		"zero sequence":     strings.Replace(valid, `"message_seq":17`, `"message_seq":0`, 1),
		"negative sequence": strings.Replace(valid, `"message_seq":17`, `"message_seq":-1`, 1),
		"overflow sequence": strings.Replace(valid, `"message_seq":17`, `"message_seq":9223372036854775808`, 1),
		"string sequence":   strings.Replace(valid, `"message_seq":17`, `"message_seq":"17"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			var got UserLocation
			if err := json.Unmarshal([]byte(raw), &got); err == nil {
				t.Fatal("invalid location accepted")
			}
		})
	}
	for _, field := range []string{"host", "recipient", "session_id", "workspace_id", "thread_id", "body", "text", "data"} {
		t.Run(field, func(t *testing.T) {
			var got UserLocation
			raw := strings.TrimSuffix(valid, "}") + `,"` + field + `":"private"}`
			if err := json.Unmarshal([]byte(raw), &got); err == nil {
				t.Fatal("untrusted field accepted")
			}
		})
	}
	var got UserLocation
	if err := json.Unmarshal([]byte(valid), &got); err != nil {
		t.Fatal(err)
	}
	host := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	recipient := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("a", 32)))
	fields, err := got.PushData(host, recipient)
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]string{"version": "1", "user_space_id": "local", "product_domain": "personal", "agent_id": "agent-main", "task_id": "task-1", "message_seq": "17", "host": host, "recipient": recipient}
	if !reflect.DeepEqual(fields, expected) {
		t.Fatalf("unexpected provider data: %#v", fields)
	}
	for _, keys := range [][2]string{{"", recipient}, {host, ""}, {"spoof", recipient}, {host, "spoof"}} {
		if _, err := got.PushData(keys[0], keys[1]); err == nil {
			t.Fatal("invalid authenticated identity accepted")
		}
	}
}

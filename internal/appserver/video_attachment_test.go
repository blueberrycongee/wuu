package appserver

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestNormalizeVideoAttachments(t *testing.T) {
	data := base64.StdEncoding.EncodeToString([]byte("video bytes"))
	for _, kind := range []string{"video/mp4", "video/webm", "video/quicktime"} {
		files, err := normalizeTurnStartFiles([]TurnStartFile{{MediaType: kind, Data: data, Filename: "clip"}})
		if err != nil || len(files) != 1 || files[0].Data != data || files[0].MediaType != kind {
			t.Fatalf("%s: %v %+v", kind, err, files)
		}
	}
	if _, err := normalizeTurnStartFiles([]TurnStartFile{{MediaType: "video/mp4", Data: "invalid!"}}); err == nil {
		t.Fatal("invalid base64 admitted")
	}
	// Each item is below the individual cap but their combined payload is not.
	large := strings.Repeat("AAAA", 4*1024*1024)
	if _, err := normalizeTurnStartFiles([]TurnStartFile{{MediaType: "video/mp4", Data: large}, {MediaType: "video/mp4", Data: large}}); err == nil {
		t.Fatal("aggregate video budget bypassed")
	}
}

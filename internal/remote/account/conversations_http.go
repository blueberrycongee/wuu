package account

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/blueberrycongee/wuu/internal/remote/conversations"
)

func (h *HTTP) conversationsHTTP(w http.ResponseWriter, r *http.Request, d Device, path string) {
	fail := func(err error) {
		status, message := 400, err.Error()
		switch {
		case errors.Is(err, ErrUnauthorized):
			status = 401
		case errors.Is(err, errSyncConflict):
			status = 409
		case errors.Is(err, sql.ErrNoRows):
			status, message = 404, "conversation not found"
		case errors.Is(err, ErrUnavailable):
			status = 503
		}
		writeJSON(w, status, map[string]string{"error": message})
	}
	decode := func(v any, size int64) bool {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, size))
		decoder.DisallowUnknownFields()
		var extra any
		if decoder.Decode(v) != nil || decoder.Decode(&extra) != io.EOF {
			fail(errors.New("invalid or oversized conversation request"))
			return false
		}
		return true
	}
	query := r.URL.Query()
	switch {
	case path == "/history/settings" && r.Method == "GET":
		result, err := h.Store.ConversationSettings(r.Context(), d, query.Get("host"), nil)
		if err != nil {
			fail(err)
			return
		}
		writeJSON(w, 200, result)
	case path == "/history/settings" && r.Method == "POST":
		var in struct {
			Host    string `json:"host"`
			Enabled *bool  `json:"enabled"`
		}
		if !decode(&in, 8192) {
			return
		}
		if in.Enabled == nil {
			fail(errors.New("enabled is required"))
			return
		}
		result, err := h.Store.ConversationSettings(r.Context(), d, in.Host, in.Enabled)
		if err != nil {
			fail(err)
			return
		}
		writeJSON(w, 200, result)
	case path == "/history" && r.Method == "GET":
		after := int64(0)
		if raw := query.Get("after"); raw != "" {
			var err error
			after, err = strconv.ParseInt(raw, 10, 64)
			if err != nil || after < 0 {
				fail(errors.New("invalid history cursor"))
				return
			}
		}
		result, err := h.Store.ConversationChanges(r.Context(), d, query.Get("host"), query.Get("generation"), after)
		if err != nil {
			fail(err)
			return
		}
		writeJSON(w, 200, result)
	case path == "/history/thread" && r.Method == "POST":
		var in conversations.Mutation
		if !decode(&in, conversations.MaxSnapshotBytes+8192) {
			return
		}
		result, err := h.Store.PutConversation(r.Context(), d, in)
		if err != nil {
			fail(err)
			return
		}
		writeJSON(w, 200, result)
	case path == "/history/thread" && r.Method == "GET":
		body, revision, err := h.Store.Conversation(r.Context(), d, query.Get("host"), query.Get("generation"), query.Get("id"))
		if err != nil {
			fail(err)
			return
		}
		writeJSON(w, 200, map[string]any{"thread": body, "revision": revision})
	default:
		writeJSON(w, 404, map[string]string{"error": "unknown conversation endpoint"})
	}
}

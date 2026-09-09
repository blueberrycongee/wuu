package account

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type HTTP struct {
	Store             *Store
	AllowRegistration bool
	Online            func(string) bool
	Changed           func(string)
	mu                sync.Mutex
	attempts          map[string]attempt
	work              chan struct{}
}
type attempt struct {
	count int
	until time.Time
}

func NewHTTP(store *Store, registration bool) *HTTP {
	return &HTTP{Store: store, AllowRegistration: registration, attempts: map[string]attempt{}, work: make(chan struct{}, 2)}
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func (h *HTTP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Native bundles and independently hosted web clients use bearer credentials;
	// no cookie authentication or credentialed CORS is accepted.
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	if r.Method == http.MethodOptions {
		w.WriteHeader(204)
		return
	}
	fail := func(err error) {
		status := 400
		if errors.Is(err, ErrUnauthorized) {
			status = 401
		}
		if errors.Is(err, ErrConflict) {
			status = 409
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
	}
	decode := func(v any) bool {
		r.Body = http.MaxBytesReader(w, r.Body, 8192)
		d := json.NewDecoder(r.Body)
		d.DisallowUnknownFields()
		if err := d.Decode(v); err != nil {
			fail(errors.New("invalid JSON request"))
			return false
		}
		var extra any
		if d.Decode(&extra) != io.EOF {
			fail(errors.New("invalid JSON request"))
			return false
		}
		return true
	}
	path := strings.TrimPrefix(r.URL.Path, "/v1/account")
	if r.Method == "GET" && path == "/config" {
		writeJSON(w, 200, map[string]any{"registration": h.AllowRegistration, "version": 1})
		return
	}
	if r.Method == "POST" && (path == "/login" || path == "/register" || path == "/recover" || path == "/password") {
		if !h.admit(r.RemoteAddr) {
			writeJSON(w, 429, map[string]string{"error": "too many attempts; try again in one minute"})
			return
		}
		select {
		case h.work <- struct{}{}:
			defer func() { <-h.work }()
		default:
			writeJSON(w, 429, map[string]string{"error": "authentication busy; retry shortly"})
			return
		}
		if path == "/login" || path == "/register" {
			if path == "/register" && !h.AllowRegistration {
				writeJSON(w, 403, map[string]string{"error": "registration is disabled by this server"})
				return
			}
			var in Login
			if !decode(&in) {
				return
			}
			session, err := h.Store.Login(in, path == "/register")
			if err != nil {
				fail(err)
				return
			}
			writeJSON(w, 200, session)
			return
		}
		var in struct {
			Username string `json:"username"`
			Secret   string `json:"secret"`
			Password string `json:"password"`
		}
		if !decode(&in) {
			return
		}
		if path == "/password" {
			d, err := h.Store.Authenticate(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
			if err != nil || d.Account != in.Username {
				fail(ErrUnauthorized)
				return
			}
		}
		recovery, err := h.Store.Reset(in.Username, in.Secret, in.Password, path == "/recover")
		if err != nil {
			fail(err)
			return
		}
		if h.Changed != nil {
			h.Changed(strings.ToLower(strings.TrimSpace(in.Username)))
		}
		writeJSON(w, 200, map[string]string{"recovery": recovery})
		return
	}
	d, err := h.Store.Authenticate(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if err != nil {
		fail(err)
		return
	}
	if path == "/devices" && r.Method == "GET" {
		devices, err := h.Store.Devices(d.Account)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": "device directory unavailable"})
			return
		}
		for i := range devices {
			if h.Online != nil {
				devices[i].Online = h.Online(devices[i].Pub)
			}
		}
		writeJSON(w, 200, map[string]any{"devices": devices, "username": d.Account})
		return
	}
	if r.Method == "DELETE" && strings.HasPrefix(path, "/devices/") || r.Method == "POST" && path == "/logout" {
		pub := strings.TrimPrefix(path, "/devices/")
		if path == "/logout" {
			pub = d.Pub
		}
		if err := h.Store.Revoke(d.Account, pub); err != nil {
			fail(err)
			return
		}
		if h.Changed != nil {
			h.Changed(d.Account)
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
		return
	}
	writeJSON(w, 404, map[string]string{"error": "unknown account endpoint"})
}
func (h *HTTP) admit(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	for key, a := range h.attempts {
		if now.After(a.until) {
			delete(h.attempts, key)
		}
	}
	a := h.attempts[host]
	if a.until.IsZero() {
		if len(h.attempts) >= 4096 {
			return false
		}
		a.until = now.Add(time.Minute)
	}
	a.count++
	h.attempts[host] = a
	return a.count <= 10
}

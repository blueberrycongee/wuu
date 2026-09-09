package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"
)

// PushEvent is a content-free notification request: the host tells the relay
// that a phone should be nudged to reconnect. The hint names a class of event
// (needs_input, agent_done); the actual content stays end-to-end encrypted
// and is fetched by the phone after it reconnects.
type PushEvent struct {
	Account string    `json:"account"`
	Device  string    `json:"device"`
	Hint    string    `json:"hint"`
	At      time.Time `json:"at"`
	Host    string    `json:"host,omitempty"`
}

// Pusher delivers push notifications to enrolled devices. Production
// deployments plug in APNs/FCM here; the relay core stays provider-agnostic.
type Pusher interface {
	Push(ctx context.Context, ev PushEvent)
}

// LogPusher writes push events to the process log. It is the default when no
// provider is configured, so the full chain stays observable end to end.
type LogPusher struct {
	Logf func(format string, args ...any)
}

func (p LogPusher) Push(_ context.Context, ev PushEvent) {
	logf := p.Logf
	if logf == nil {
		logf = log.Printf
	}
	logf("relay push: account=%s device=%s hint=%s", shortKey(ev.Account), shortKey(ev.Device), ev.Hint)
}

// WebhookPusher POSTs push events as JSON to an external endpoint, letting a
// deployment bridge to any notification provider without touching the relay.
type WebhookPusher struct {
	URL    string
	Client *http.Client
}

func (p WebhookPusher) Push(ctx context.Context, ev PushEvent) {
	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	body, err := json.Marshal(ev)
	if err != nil {
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.URL, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}

func shortKey(k string) string {
	if len(k) <= 12 {
		return k
	}
	return k[:12] + "..."
}

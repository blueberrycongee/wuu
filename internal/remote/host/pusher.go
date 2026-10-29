package host

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/blueberrycongee/wuu/internal/appserver"
	"github.com/blueberrycongee/wuu/internal/remote/wire"
)

// HostPushEvent is a content-free notification request: the host tells the
// configured HostPusher that a phone should be nudged. Hint names a class
// of event (agent_done, needs_input); the actual content stays
// end-to-end encrypted and is fetched by the phone after it reconnects.
//
// Token and Platform come from the device's stored push registration. ThreadID
// is set for Coding turn/completed or turn/error notifications. Personal uses
// UserLocation only after a policy-admitted user/attention notification.
type HostPushEvent struct {
	Host         string             // authenticated host public key
	UserLocation *wire.UserLocation // Personal destination; excludes ThreadID
	Device       string             // device pub
	Token        string             // push token (opaque to the host)
	Platform     string             // "ios" | "android"
	Hint         string             // wire.PushAgentDone | wire.PushNeedsInput
	ThreadID     string             // optional, present for turn-bound hints
	At           time.Time
}

// HostPusher delivers a content-free notification to one paired device.
// Production deployments wire ExpoPusher (or a future direct APNs/FCM
// bridge) here. The host calls this directly because only the host has
// the device's push token in its store; the relay-side Pusher is
// provider-agnostic and never sees the token.
type HostPusher interface {
	Push(ctx context.Context, ev HostPushEvent)
}

// devicePushRegistrar binds one paired device's app-server device/push_*
// RPCs to the host store, keyed by the device pub the session authenticated
// with. The app-server never sees the pub; this adapter is what makes the
// registration land on the right paired-device record.
type devicePushRegistrar struct {
	store  *Store
	devPub string
}

func (r devicePushRegistrar) RegisterDevicePush(params appserver.DevicePushRegisterParams) error {
	return r.store.SetDevicePushToken(r.devPub, params.Token, params.Platform, time.Now().UTC())
}

func (r devicePushRegistrar) UnregisterDevicePush(appserver.DevicePushUnregisterParams) error {
	return r.store.SetDevicePushToken(r.devPub, "", "", time.Now().UTC())
}

// LogHostPusher writes push events to the process log instead of delivering
// them. Tests and embeddings that must not reach a push provider opt into it
// explicitly; the host defaults to ExpoPusher so registered devices actually
// get notified.
type LogHostPusher struct {
	Logf func(format string, args ...any)
}

func (p LogHostPusher) Push(_ context.Context, ev HostPushEvent) {
	logf := p.Logf
	if logf == nil {
		logf = log.Printf
	}
	logf("host push: device=%s hint=%s thread=%s platform=%s", shortPub(ev.Device), ev.Hint, ev.ThreadID, ev.Platform)
}

func shortPub(pub string) string {
	if len(pub) <= 12 {
		return pub
	}
	return pub[:12] + "..."
}

// ExpoPushRequest is one entry in the Expo Push Service batch payload. The
// API accepts an array of these and returns one ticket per entry. We send
// at most one entry per call (the host's per-device throttle already
// enforces the rate), so the response map is size one.
type ExpoPushRequest struct {
	To       string         `json:"to"`    // Expo push token
	Title    string         `json:"title"` // shown in the notification
	Body     string         `json:"body"`  // shown beneath the title
	Sound    string         `json:"sound,omitempty"`
	Data     map[string]any `json:"data,omitempty"` // free-form, e.g. {"thread_id": "..."}
	Priority string         `json:"priority,omitempty"`
	TTL      *int           `json:"ttl,omitempty"`
}

// ExpoPushResponse is one entry of the Expo Push Service's reply. Status
// is "ok" on success or an error code (e.g. "DeviceNotRegistered"). Data
// is set when the server returns ticket metadata.
type ExpoPushResponse struct {
	// Status of the push. Possible values: "ok", "error".
	Status string `json:"status"`
	// ID of the ticket on success.
	ID string `json:"id,omitempty"`
	// Message is the human-readable error message on failure.
	Message string `json:"message,omitempty"`
	// Details may contain extra structured error data.
	Details map[string]any `json:"details,omitempty"`
}

// ExpoPusher posts content-free push events to the Expo Push Service. It
// is the default production integration: a phone obtains an Expo push
// token (ExponentPushToken[…]) at boot and publishes it via
// device/push_register; the host then posts to https://exp.host/--/api/v2/push/send
// with the registered token and a hint-derived title/body.
//
// Configurable knobs:
//   - Endpoint: the Expo API URL. Override to point at a test environment
//     or a self-hosted push relay.
//   - HTTPClient: tests can plug in a transport. Production uses the
//     10-second default.
//
// The pusher is safe for concurrent use.
type ExpoPusher struct {
	Endpoint   string
	HTTPClient *http.Client
	// Now lets tests inject a clock. nil falls back to time.Now.
	Now func() time.Time
}

// DefaultExpoEndpoint is the production Expo Push Service URL.
const DefaultExpoEndpoint = "https://exp.host/--/api/v2/push/send"

// NewExpoPusher returns an ExpoPusher with sensible defaults.
func NewExpoPusher() *ExpoPusher {
	return &ExpoPusher{
		Endpoint:   DefaultExpoEndpoint,
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// Push sends one content-free notification. Tokens without the
// "ExponentPushToken[" / "ExpoPushToken[" prefix are skipped silently
// because they cannot be delivered to the Expo backend — this lets the
// host register non-Expo tokens (e.g. raw APNs/FCM) without crashing.
func (p *ExpoPusher) Push(ctx context.Context, ev HostPushEvent) {
	if !isExpoToken(ev.Token) {
		return
	}
	endpoint := p.Endpoint
	if endpoint == "" {
		endpoint = DefaultExpoEndpoint
	}
	client := p.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	title, body := expoCopy(ev.Hint, ev.ThreadID)
	data := map[string]any{"thread_id": ev.ThreadID, "hint": ev.Hint}
	if ev.UserLocation != nil {
		if ev.Hint != wire.PushAgentDone || ev.ThreadID != "" {
			return
		}
		fields, err := ev.UserLocation.PushData(ev.Host, ev.Device)
		if err != nil {
			return
		}
		data = make(map[string]any, len(fields))
		for key, value := range fields {
			data[key] = value
		}
	} else if ev.ThreadID != "" {
		// The mobile tap handler consumes data.url as a wuu:// deep link
		// (clients/mobile startPushListeners); thread_id stays alongside it
		// for consumers that want the raw id.
		data["url"] = "wuu://thread/" + ev.ThreadID
	}
	req := ExpoPushRequest{
		To:       ev.Token,
		Title:    title,
		Body:     body,
		Sound:    "default",
		Data:     data,
		Priority: "high",
	}
	payload, err := json.Marshal([]ExpoPushRequest{req})
	if err != nil {
		return
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(httpReq)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return
	}
	var out []ExpoPushResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return
	}
	for _, e := range out {
		if e.Status != "ok" {
			// Logged silently: a failed push is recoverable because the
			// phone will pick up new turns on its next reconnect anyway.
			// The host's pusher logs go to stderr by default.
			fmt.Printf("expo push: status=%s msg=%s device=%s\n", e.Status, e.Message, shortPub(ev.Device))
		}
	}
}

// isExpoToken reports whether the token looks like an Expo push token. We
// accept both the legacy "ExponentPushToken[…]" form and the newer
// "ExpoPushToken[…]" form. Real tokens are roughly 41+ characters of
// base64-looking text inside the brackets.
func isExpoToken(token string) bool {
	if len(token) < 22 {
		return false
	}
	return hasPrefix(token, "ExponentPushToken[") || hasPrefix(token, "ExpoPushToken[")
}

func hasPrefix(s, p string) bool {
	if len(s) < len(p) {
		return false
	}
	return s[:len(p)] == p
}

// expoCopy renders a hint into a user-visible title + body. Push
// notifications are deliberately content-free; only the thread id and a
// generic class word ("agent done" / "needs input") leak through.
func expoCopy(hint, threadID string) (title, body string) {
	switch hint {
	case "needs_input":
		return "wuu", "Agent needs your input"
	case "agent_done":
		return "wuu", "Agent finished a turn"
	default:
		return "wuu", "wuu activity"
	}
}

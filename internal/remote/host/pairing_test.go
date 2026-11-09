package host

import (
	"github.com/blueberrycongee/wuu/internal/remote/wire"
	"path/filepath"
	"testing"
)

func TestPairingURIWaitsForMatchingRelayAcknowledgement(t *testing.T) {
	store, err := LoadOrCreateStore(filepath.Join(t.TempDir(), "remote.json"), "test")
	if err != nil {
		t.Fatal(err)
	}
	h := &Host{store: store, relayURL: "ws://localhost/v1/connect"}
	var shown []string
	uri, err := h.StartPairing(PairingConfig{OnURI: func(uri string) { shown = append(shown, uri) }})
	if err != nil {
		t.Fatal(err)
	}
	if len(shown) != 0 {
		t.Fatal("pairing was advertised before relay registration")
	}
	h.dispatch(wire.RelayMsg{Type: wire.TypeOK, PairingID: "stale"})
	if len(shown) != 0 {
		t.Fatal("stale acknowledgement advertised current pairing")
	}
	ack := wire.RelayMsg{Type: wire.TypeOK, PairingID: h.pairing.pairing.ID}
	h.dispatch(ack)
	h.dispatch(ack)
	if len(shown) != 1 || shown[0] != uri {
		t.Fatalf("announcements = %v", shown)
	}
}

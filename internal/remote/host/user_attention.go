package host

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/blueberrycongee/wuu/internal/appserver"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/remote/account"
	"github.com/blueberrycongee/wuu/internal/remote/secure"
)

// Bound one attention delivery pass, including account lookup and relay writes.
const maxAttentionDirectoryRecords = 256

func (h *Host) runUserAttention(ctx context.Context, relayReady <-chan struct{}) error {
	if h.subscribeUserAttention == nil {
		return nil
	}
	// Preserve the authority's bounded startup replay until a relay can accept it.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-relayReady:
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return h.subscribeUserAttention(principal.WithContext(ctx, principal.LocalOwner()), func(event appserver.UserAttentionNotification) {
		if err := h.pushUserAttention(ctx, event); err != nil && ctx.Err() == nil {
			h.logf("remote user attention: %v", err)
		}
	})
}

func (h *Host) pushUserAttention(ctx context.Context, event appserver.UserAttentionNotification) error {
	if h.authorizeUserAttention == nil {
		return errors.New("user attention resource policy is unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, _ := json.Marshal(struct {
		Method string                              `json:"method"`
		Params appserver.UserAttentionNotification `json:"params"`
	}{appserver.NotificationUserAttention, event})
	hint, _, location := classifyPushHint(raw)
	if hint == "" || location == nil {
		return errors.New("invalid admitted user attention")
	}
	recipients, accountOnly, err := h.userAttentionRecipients(ctx)
	if err != nil {
		return err
	}
	for _, pub := range recipients {
		if err := ctx.Err(); err != nil {
			return err
		}
		caller, err := h.devicePrincipal(pub)
		if err != nil {
			continue
		}
		authorize := func(deliveryCtx context.Context) error {
			return h.authorizeUserAttention(principal.WithContext(deliveryCtx, caller), event)
		}
		if err := authorize(ctx); err != nil {
			continue
		}
		session := h.ensureSession(pub)
		session.mu.Lock()
		push := false
		if !session.attached || session.channel == nil {
			push = session.tryConsumePushSlot(hint, "personal:"+location.AgentID)
		}
		session.mu.Unlock()
		if push {
			h.sendPushContext(ctx, pub, hint, "", location, accountOnly, authorize)
		}
	}
	return ctx.Err()
}

func (h *Host) userAttentionRecipients(ctx context.Context) ([]string, bool, error) {
	credentials := h.store.Account()
	if credentials == nil {
		devices := h.store.Devices()
		if len(devices) > maxAttentionDirectoryRecords {
			return nil, false, errors.New("too many paired attention recipients")
		}
		recipients := make([]string, 0, len(devices))
		for _, device := range devices {
			recipients = append(recipients, device.Pub)
		}
		return recipients, false, nil
	}
	if credentials.Username == "" || credentials.Token == "" {
		return nil, true, account.ErrUnauthorized
	}
	var directory struct {
		Devices []account.Device `json:"devices"`
	}
	if err := account.Request(ctx, credentials.Server, credentials.Token, "GET", "/devices", nil, &directory); err != nil {
		return nil, true, err
	}
	if len(directory.Devices) > maxAttentionDirectoryRecords {
		return nil, true, errors.New("too many account attention recipients")
	}
	host := secure.EncodeKey(h.store.Identity().Public())
	hostAllowed := false
	recipients := []string{}
	seen := map[string]bool{}
	for _, device := range directory.Devices {
		if device.Account != credentials.Username {
			continue
		}
		if device.Pub == host && device.Role == "host" {
			hostAllowed = true
		}
		if device.Role != "phone" || seen[device.Pub] {
			continue
		}
		pub, err := secure.DecodeKey(device.Pub)
		if err != nil || secure.EncodeKey(pub) != device.Pub {
			continue
		}
		seen[device.Pub] = true
		recipients = append(recipients, device.Pub)
	}
	if !hostAllowed {
		return nil, true, account.ErrUnauthorized
	}
	return recipients, true, nil
}

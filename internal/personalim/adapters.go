package personalim

import (
	"context"
	"errors"
	"sync"
)

// Credentials are supplied by trusted composition, never persisted in messaging
// settings or exposed to the Personal model or desktop renderer.
type Credentials struct{ SlackAppToken, SlackBotToken, FeishuAppID, FeishuAppSecret string }

func DefaultDrivers(c Credentials, status func(string, ConnectionState)) map[string]Driver {
	drivers := map[string]Driver{}
	if c.SlackAppToken != "" && c.SlackBotToken != "" {
		s := &Slack{AppToken: c.SlackAppToken, BotToken: c.SlackBotToken}
		if status != nil {
			s.OnStatus = func(v ConnectionState) { status("slack", v) }
		}
		drivers["slack"] = &slackDriver{s}
	}
	if c.FeishuAppID != "" && c.FeishuAppSecret != "" {
		f := &Feishu{AppID: c.FeishuAppID, AppSecret: c.FeishuAppSecret}
		if status != nil {
			f.OnStatus = func(v ConnectionState) { status("feishu", v) }
		}
		drivers["feishu"] = &feishuDriver{Feishu: f}
	}
	return drivers
}

type slackDriver struct{ *Slack }

func (s *slackDriver) Validate(ctx context.Context, b Binding) error {
	identity, err := s.Identity(ctx)
	if err != nil {
		return err
	}
	if identity.TeamID != b.TenantID {
		return errors.New("Slack credentials belong to a different workspace")
	}
	if err = s.ValidateConversation(ctx, b.ConversationID); err != nil {
		return err
	}
	var result struct {
		User struct {
			ID      string `json:"id"`
			Deleted bool   `json:"deleted"`
			IsBot   bool   `json:"is_bot"`
		} `json:"user"`
	}
	if err = s.call(ctx, "users.info", s.BotToken, map[string]string{"user": b.SenderID}, &result, false); err != nil {
		return err
	}
	if result.User.ID != b.SenderID || result.User.Deleted || result.User.IsBot {
		return errors.New("Slack owner must be the selected active human user")
	}
	return nil
}

type feishuDriver struct {
	*Feishu
	mu     sync.Mutex
	tenant string
}

func (f *feishuDriver) Validate(ctx context.Context, b Binding) error {
	copy := f.forTenant(b.TenantID)
	if err := copy.ValidateBinding(ctx, b.TenantID, b.SenderID, b.ConversationID); err != nil {
		return err
	}
	f.mu.Lock()
	f.tenant = b.TenantID
	f.mu.Unlock()
	return nil
}
func (f *feishuDriver) Run(ctx context.Context, accept func(context.Context, Incoming) error) error {
	f.mu.Lock()
	copy := f.forTenant(f.tenant)
	f.mu.Unlock()
	return copy.Run(ctx, accept)
}

// Each connection owns its token lock/cache; only immutable configuration is shared.
func (f *feishuDriver) forTenant(tenant string) *Feishu {
	return &Feishu{AppID: f.AppID, AppSecret: f.AppSecret, TenantID: tenant, APIURL: f.APIURL, HTTPClient: f.HTTPClient, OnStatus: f.OnStatus}
}

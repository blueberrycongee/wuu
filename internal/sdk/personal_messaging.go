package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"os"

	"github.com/blueberrycongee/wuu/internal/appserver"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/executionauth"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/personalim"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

func personalMessagingFactory(opts Options) appserver.PersonalMessagingFactory {
	return func(ctx context.Context, input runtime.NativeProviderContext, host *appserver.UserHost, current func() bool) (*personalim.Bridge, error) {
		call := func(ctx context.Context, service, method string, raw json.RawMessage) (json.RawMessage, error) {
			result, err := input.Registry.Call(ctx, appserver.PersonalMessagingModule, pluginhost.ServiceCallParams{Service: service, Method: method, Params: raw})
			if err != nil {
				return nil, err
			}
			return result, nil
		}
		credentials := credentialstore.NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
			return call(ctx, credentialstore.ServiceName, method, raw)
		})
		documents := persistence.NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
			return call(ctx, persistence.ServiceName, method, raw)
		})
		authorization := executionauth.NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
			state, err := input.RetainState()
			if err != nil {
				return nil, err
			}
			defer state.Close()
			request, err := json.Marshal(runtime.ExecutionAuthorizationIntent{Operation: method, Params: raw})
			if err != nil {
				return nil, err
			}
			return host.HandleOwnerEnrollment(runtime.WithInvocationState(ctx, state), appserver.PersonalMessagingModule, request)
		})
		values := opts.PersonalMessaging
		slots := []struct {
			key, env string
			value    *string
		}{{"slack-app-token", "WUU_SLACK_APP_TOKEN", &values.SlackAppToken}, {"slack-bot-token", "WUU_SLACK_BOT_TOKEN", &values.SlackBotToken}, {"feishu-app-id", "WUU_FEISHU_APP_ID", &values.FeishuAppID}, {"feishu-app-secret", "WUU_FEISHU_APP_SECRET", &values.FeishuAppSecret}}
		credentialError := ""
		for _, slot := range slots {
			if opts.PersonalMessagingDrivers != nil {
				break
			}
			if *slot.value == "" {
				*slot.value = os.Getenv(slot.env)
			}
			if *slot.value != "" {
				if err := credentials.Set(ctx, "personal-messaging", slot.key, []byte(*slot.value)); err != nil {
					credentialError = "selected_credential_store_unavailable"
					break
				}
				continue
			}
			value, err := credentials.Get(ctx, "personal-messaging", slot.key)
			if err != nil && !errors.Is(err, credentialstore.ErrNotFound) {
				credentialError = "selected_credential_store_unavailable"
				break
			}
			*slot.value = string(value)
			clear(value)
		}
		enrollment := func(b personalim.Binding) executionauth.OwnerEnrollment {
			return executionauth.OwnerEnrollment{ID: b.EnrollmentID, Principal: principal.Identity{Kind: "user", Issuer: "wuu:im:" + b.Platform, Tenant: b.TenantID, Subject: b.SenderID}, UserSpaceID: input.UserBinding.UserSpaceID, ProductDomain: input.UserBinding.ProductDomain, ConversationID: b.ConversationID}
		}
		options := personalim.Options{Documents: documents, CredentialError: credentialError, Current: current,
			Enroll: func(ctx context.Context, b personalim.Binding) (personalim.Binding, error) {
				request := enrollment(b)
				result, err := authorization.EnrollOwner(ctx, request)
				if err != nil {
					return b, err
				}
				if result != request || result.Revoked {
					return b, errors.New("owner enrollment response changed binding")
				}
				return b, nil
			},
			Revoke: func(ctx context.Context, b personalim.Binding) error {
				request := executionauth.OwnerEnrollmentRef{ID: b.EnrollmentID}
				result, err := authorization.RevokeOwner(ctx, request)
				if err == nil && !result.Revoked {
					return errors.New("owner enrollment revocation unconfirmed")
				}
				return err
			},
			Client: func(ctx context.Context, b personalim.Binding) (personalim.Client, error) {
				return &messagingChannelClient{host: host, input: input, binding: b}, nil
			},
		}
		var bridge *personalim.Bridge
		options.Drivers = opts.PersonalMessagingDrivers
		if credentialError != "" {
			options.Drivers = map[string]personalim.Driver{}
		}
		if options.Drivers == nil {
			options.Drivers = personalim.DefaultDrivers(values, func(platform string, state personalim.ConnectionState) {
				if bridge != nil {
					bridge.ObserveConnection(platform, state)
				}
			})
		}
		var err error
		bridge, err = personalim.New(options)
		return bridge, err
	}
}

// messagingChannelClient uses the same host ingress as external selected
// messaging providers. It never constructs a principal or submission proof.
type messagingChannelClient struct {
	host    *appserver.UserHost
	input   runtime.NativeProviderContext
	binding personalim.Binding
}

func (c *messagingChannelClient) Call(ctx context.Context, method string, input json.RawMessage) (json.RawMessage, error) {
	state, err := c.input.RetainState()
	if err != nil {
		return nil, err
	}
	defer state.Close()
	raw, err := json.Marshal(appserver.PersonalChannelRequest{Binding: c.binding, Method: method, Input: input})
	if err != nil {
		return nil, err
	}
	return c.host.HandlePersonalChannel(runtime.WithInvocationState(ctx, state), appserver.PersonalMessagingModule, raw)
}
func (*messagingChannelClient) Close() error { return nil }

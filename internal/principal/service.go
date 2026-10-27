package principal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
)

const ServiceName = "identity.authentication"
const ServiceMajor = 1

type Client struct {
	call func(context.Context, string, json.RawMessage) (json.RawMessage, error)
}

func NewClient(call func(context.Context, string, json.RawMessage) (json.RawMessage, error)) *Client {
	return &Client{call: call}
}
func (c *Client) Authenticate(ctx context.Context, credential Credential) (Authentication, error) {
	if credential.Scheme == "" || credential.Token == "" || credential.Audience == "" {
		return Authentication{}, errors.New("transport credential is incomplete")
	}
	raw, err := json.Marshal(credential)
	if err != nil {
		return Authentication{}, err
	}
	result, err := c.call(ctx, "authenticate", raw)
	if err != nil {
		return Authentication{}, err
	}
	var resultIdentity Authentication
	dec := json.NewDecoder(bytes.NewReader(result))
	dec.DisallowUnknownFields()
	if dec.Decode(&resultIdentity) != nil || dec.Decode(new(any)) != io.EOF {
		return Authentication{}, errors.New("invalid identity provider result")
	}
	return resultIdentity, resultIdentity.Identity.Validate()
}

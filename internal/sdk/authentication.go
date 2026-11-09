package sdk

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/blueberrycongee/wuu/internal/principal"
	"io"
	"strings"
)

const authenticationFrameLimit = 64 * 1024

// ServeProviderAuthenticated is the public CLI's untrusted JSONL ingress. The
// process launch fixes this mode and audience; the peer cannot request owner
// authority. Credentials stay on the input channel, never argv or diagnostics.
func (r *Runtime) ServeProviderAuthenticated(ctx context.Context, in io.Reader, out io.Writer, audience string) error {
	buffered, id, credential, err := readAuthenticationHandshake(in, audience)
	if err != nil {
		_ = json.NewEncoder(out).Encode(map[string]any{"id": id, "error": map[string]string{"code": "authentication_failed", "message": "a valid authentication v1 handshake is required"}})
		return errors.New("authentication handshake rejected")
	}
	return r.serve(ctx, buffered, out, &credential, id, false)
}
func readAuthenticationHandshake(in io.Reader, audience string) (*bufio.Reader, json.RawMessage, principal.Credential, error) {
	if strings.TrimSpace(audience) == "" {
		return nil, nil, principal.Credential{}, errors.New("configured authentication audience is required")
	}
	buffered := bufio.NewReaderSize(in, authenticationFrameLimit)
	raw, err := buffered.ReadSlice('\n')
	if err != nil {
		return nil, nil, principal.Credential{}, errors.New("authentication frame missing or too large")
	}
	var frame struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			Version    int    `json:"version"`
			Scheme     string `json:"scheme"`
			Credential string `json:"credential"`
		} `json:"params"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&frame) != nil || decoder.Decode(new(any)) != io.EOF || frame.Method != "authenticate" || frame.Params.Version != 1 || frame.Params.Scheme == "" || frame.Params.Credential == "" || len(frame.Params.Credential) > 48*1024 || len(frame.ID) == 0 {
		return nil, frame.ID, principal.Credential{}, errors.New("invalid authentication frame")
	}
	return buffered, frame.ID, principal.Credential{Scheme: frame.Params.Scheme, Token: frame.Params.Credential, Audience: audience}, nil
}

package account

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Credentials struct {
	Server   string `json:"server"`
	Token    string `json:"token"`
	Username string `json:"username"`
}

// Origin requires transport security except for a process on this machine.
func Origin(raw string) (string, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(raw), "/"))
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return "", errors.New("server must be an HTTPS origin")
	}
	if u.Scheme != "https" && (u.Scheme != "http" || (u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" && u.Hostname() != "::1")) {
		return "", errors.New("HTTPS is required except on localhost")
	}
	return u.String(), nil
}

type RequestError struct {
	Status  int
	Message string
}

func (e *RequestError) Error() string {
	return fmt.Sprintf("account server (%d): %s", e.Status, e.Message)
}
func Unauthorized(err error) bool {
	var e *RequestError
	return errors.As(err, &e) && e.Status == http.StatusUnauthorized
}

func Request(ctx context.Context, server, token, method, path string, in, out any) error {
	origin, err := Origin(server)
	if err != nil {
		return err
	}
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, origin+"/v1/account"+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}
	if res.StatusCode != 200 {
		var msg struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &msg)
		return &RequestError{Status: res.StatusCode, Message: msg.Error}
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}
func (c Credentials) Allows(ctx context.Context, host, phone string) bool {
	var result struct {
		Devices []Device `json:"devices"`
	}
	if Request(ctx, c.Server, c.Token, "GET", "/devices", nil, &result) != nil {
		return false
	}
	hostOK, phoneOK := false, false
	for _, d := range result.Devices {
		if d.Account != c.Username {
			continue
		}
		hostOK = hostOK || (d.Pub == host && d.Role == "host")
		phoneOK = phoneOK || (d.Pub == phone && d.Role == "phone")
	}
	return hostOK && phoneOK
}

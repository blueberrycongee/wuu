package relay

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/remote/account"
)

// NativePushConfig belongs to the deployment operator, never a phone or host.
// Provider secrets are loaded from files and are not exposed by account APIs.
type NativePushConfig struct {
	APNs *struct {
		KeyFile string `json:"key_file"`
		KeyID   string `json:"key_id"`
		TeamID  string `json:"team_id"`
		Topic   string `json:"topic"`
		Sandbox bool   `json:"sandbox"`
	} `json:"apns"`
	FCM *struct {
		ServiceAccountFile string `json:"service_account_file"`
	} `json:"fcm"`
}
type NativePusher struct {
	store                 *account.Store
	config                NativePushConfig
	apnsKey               *ecdsa.PrivateKey
	fcmKey                *rsa.PrivateKey
	fcmEmail, fcmProject  string
	client                *http.Client
	logf                  func(string, ...any)
	slots                 chan struct{}
	mu                    sync.Mutex
	apnsJWT, fcmToken     string
	apnsExpiry, fcmExpiry time.Time
}

func NewNativePusher(path string, store *account.Store, logf func(string, ...any)) (*NativePusher, error) {
	if store == nil {
		return nil, errors.New("native push requires account authentication")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	p := &NativePusher{store: store, client: &http.Client{Timeout: 15 * time.Second}, logf: logf, slots: make(chan struct{}, 8)}
	if err = json.Unmarshal(data, &p.config); err != nil {
		return nil, err
	}
	if c := p.config.APNs; c != nil {
		if c.KeyID == "" || c.TeamID == "" || !regexp.MustCompile(`^[A-Za-z0-9.-]+$`).MatchString(c.Topic) {
			return nil, errors.New("invalid APNs configuration")
		}
		key, err := loadPushKey(c.KeyFile)
		if err != nil {
			return nil, err
		}
		var ok bool
		p.apnsKey, ok = key.(*ecdsa.PrivateKey)
		if !ok || p.apnsKey.Curve != elliptic.P256() {
			return nil, errors.New("APNs requires a P-256 PKCS8 key")
		}
	}
	if c := p.config.FCM; c != nil {
		data, err := os.ReadFile(c.ServiceAccountFile)
		if err != nil {
			return nil, err
		}
		var service struct {
			PrivateKey  string `json:"private_key"`
			ClientEmail string `json:"client_email"`
			ProjectID   string `json:"project_id"`
		}
		if err = json.Unmarshal(data, &service); err != nil {
			return nil, err
		}
		if service.ClientEmail == "" || !regexp.MustCompile(`^[a-z0-9-]+$`).MatchString(service.ProjectID) {
			return nil, errors.New("invalid FCM service account")
		}
		key, err := parsePushKey([]byte(service.PrivateKey))
		if err != nil {
			return nil, err
		}
		var ok bool
		p.fcmKey, ok = key.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("FCM requires an RSA PKCS8 key")
		}
		p.fcmEmail, p.fcmProject = service.ClientEmail, service.ProjectID
	}
	if len(p.Platforms()) == 0 {
		return nil, errors.New("configure APNs or FCM")
	}
	return p, nil
}
func loadPushKey(path string) (any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parsePushKey(data)
}
func parsePushKey(data []byte) (any, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("invalid push private key")
	}
	return x509.ParsePKCS8PrivateKey(block.Bytes)
}
func (p *NativePusher) Platforms() []string {
	result := []string{}
	if p.apnsKey != nil {
		result = append(result, "ios")
	}
	if p.fcmKey != nil {
		result = append(result, "android")
	}
	return result
}

func (p *NativePusher) Push(_ context.Context, event PushEvent) {
	select {
	case p.slots <- struct{}{}:
	default:
		if p.logf != nil {
			p.logf("native push: delivery queue full")
		}
		return
	}
	go func() {
		defer func() { <-p.slots }()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		registration, ok := p.store.Push(event.Account, event.Device)
		if !ok {
			return
		}
		// Recheck ownership after queueing, including a host revoked in the meantime.
		host, ok := p.store.Device(event.Host)
		if !ok || host.Account != event.Account || host.Role != "host" {
			return
		}
		if err := p.deliver(ctx, event, registration); err != nil && p.logf != nil {
			p.logf("native push %s: %v", registration.Platform, err)
		}
	}()
}
func pushJWT(header, claims any, key any) (string, error) {
	h, _ := json.Marshal(header)
	c, _ := json.Marshal(claims)
	enc := base64.RawURLEncoding
	message := enc.EncodeToString(h) + "." + enc.EncodeToString(c)
	digest := sha256.Sum256([]byte(message))
	var signature []byte
	switch key := key.(type) {
	case *ecdsa.PrivateKey:
		r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
		if err != nil {
			return "", err
		}
		signature = make([]byte, 64)
		r.FillBytes(signature[:32])
		s.FillBytes(signature[32:])
	case *rsa.PrivateKey:
		var err error
		signature, err = rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
		if err != nil {
			return "", err
		}
	default:
		return "", errors.New("unsupported push signing key")
	}
	return message + "." + enc.EncodeToString(signature), nil
}
func (p *NativePusher) authorization(ctx context.Context, platform string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	if platform == "ios" {
		if p.apnsKey == nil {
			return "", errors.New("APNs is not configured")
		}
		if now.Before(p.apnsExpiry) {
			return p.apnsJWT, nil
		}
		token, err := pushJWT(map[string]string{"alg": "ES256", "kid": p.config.APNs.KeyID}, map[string]any{"iss": p.config.APNs.TeamID, "iat": now.Unix()}, p.apnsKey)
		if err == nil {
			p.apnsJWT = token
			p.apnsExpiry = now.Add(45 * time.Minute)
		}
		return token, err
	}
	if p.fcmKey == nil {
		return "", errors.New("FCM is not configured")
	}
	if now.Before(p.fcmExpiry) {
		return p.fcmToken, nil
	}
	const endpoint = "https://oauth2.googleapis.com/token"
	assertion, err := pushJWT(map[string]string{"alg": "RS256", "typ": "JWT"}, map[string]any{"iss": p.fcmEmail, "scope": "https://www.googleapis.com/auth/firebase.messaging", "aud": endpoint, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix()}, p.fcmKey)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"}, "assertion": {assertion}}.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := p.client.Do(req)
	if err != nil {
		return "", errors.New("FCM authorization transport failed")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return "", fmt.Errorf("FCM authorization HTTP %d", response.StatusCode)
	}
	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 65536)).Decode(&result); err != nil || result.AccessToken == "" || result.ExpiresIn <= 60 {
		return "", errors.New("invalid FCM authorization response")
	}
	p.fcmToken = result.AccessToken
	p.fcmExpiry = now.Add(time.Duration(min(result.ExpiresIn, 3600)-60) * time.Second)
	return p.fcmToken, nil
}
func (p *NativePusher) deliver(ctx context.Context, event PushEvent, registration account.PushRegistration) error {
	token, err := p.authorization(ctx, registration.Platform)
	if err != nil {
		return err
	}
	body := "Agent 已完成，请打开 Wuu 查看。"
	if event.Hint == "needs_input" {
		body = "Agent 需要你的输入，请打开 Wuu。"
	}
	var endpoint string
	var payload any
	if registration.Platform == "ios" {
		if !regexp.MustCompile(`^[a-fA-F0-9]{32,512}$`).MatchString(registration.Token) {
			return errors.New("invalid APNs device token")
		}
		endpoint = "https://api.push.apple.com/3/device/" + registration.Token
		if p.config.APNs.Sandbox {
			endpoint = "https://api.sandbox.push.apple.com/3/device/" + registration.Token
		}
		payload = map[string]any{"aps": map[string]any{"alert": map[string]string{"title": "Wuu", "body": body}, "sound": "default"}, "host": event.Host}
	} else {
		endpoint = "https://fcm.googleapis.com/v1/projects/" + p.fcmProject + "/messages:send"
		payload = map[string]any{"message": map[string]any{"token": registration.Token, "notification": map[string]string{"title": "Wuu", "body": body}, "data": map[string]string{"host": event.Host}, "android": map[string]any{"priority": "high", "ttl": "300s"}}}
	}
	data, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	if registration.Platform == "ios" {
		req.Header.Set("apns-topic", p.config.APNs.Topic)
		req.Header.Set("apns-push-type", "alert")
		req.Header.Set("apns-priority", "10")
		req.Header.Set("apns-expiration", fmt.Sprint(time.Now().Add(5*time.Minute).Unix()))
	}
	response, err := p.client.Do(req)
	if err != nil {
		return errors.New("notification provider transport failed")
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 65536))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("notification provider HTTP %d", response.StatusCode)
	}
	return nil
}

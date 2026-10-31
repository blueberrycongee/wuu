// Package plugincontrol coordinates local development publication with the
// actual app-server hosts. Registry entries are discovery hints; live responses
// are authoritative and an OS-held lease decides whether a host still exists.
package plugincontrol

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/securefs"
	"github.com/blueberrycongee/wuu/internal/storelock"
)

type HostInfo struct {
	ID            string `json:"id"`
	PID           int    `json:"pid"`
	Workdir       string `json:"workdir"`
	WorkspaceID   string `json:"workspace_id,omitempty"`
	UserSpaceID   string `json:"user_space_id,omitempty"`
	ProductDomain string `json:"product_domain"`
}

type Endpoint struct {
	Host    HostInfo `json:"host"`
	Address string   `json:"address"`
	Token   string   `json:"token"`
}

type PluginStatus struct {
	ID               string `json:"id"`
	SubjectID        string `json:"subject_id"`
	StorageSubjectID string `json:"storage_subject_id"`
	Fingerprint      string `json:"fingerprint"`
	State            string `json:"state"`
	Error            string `json:"error,omitempty"`
}

type Request struct {
	Operation       string `json:"operation"`
	PluginID        string `json:"plugin_id,omitempty"`
	Fingerprint     string `json:"fingerprint,omitempty"`
	ManifestPath    string `json:"manifest_path,omitempty"`
	SourceDirectory string `json:"source_directory,omitempty"`
}

type Response struct {
	Host         HostInfo       `json:"host"`
	GenerationID string         `json:"generation_id,omitempty"`
	Plugins      []PluginStatus `json:"plugins"`
	Error        string         `json:"error,omitempty"`
}

func (r Response) ActivationError(id, fingerprint string) error {
	for _, status := range r.Plugins {
		if status.ID != id {
			continue
		}
		if status.State == "inactive" {
			return nil
		}
		if status.State != "active" {
			return fmt.Errorf("plugin %s is %s: %s", id, status.State, status.Error)
		}
		if fingerprint != "" && status.Fingerprint != fingerprint {
			return fmt.Errorf("plugin %s has not adopted published fingerprint %s", id, fingerprint)
		}
		return nil
	}
	return fmt.Errorf("plugin %s is absent from the host catalog", id)
}

type Registration struct {
	server *http.Server
	lease  *storelock.Lock
	path   string
	once   sync.Once
}

func Register(home string, info HostInfo, handler func(context.Context, Request) Response) (*Registration, error) {
	root := filepath.Join(home, "run", "plugin-hosts")
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(root, "host-")
	if err != nil {
		return nil, err
	}
	lease, err := storelock.AcquireContext(context.Background(), dir)
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		_ = lease.Release()
		return nil, err
	}
	info.ID, info.PID = filepath.Base(dir), os.Getpid()
	endpoint := Endpoint{Host: info, Address: listener.Addr().String(), Token: rand.Text()}
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 10 * time.Second}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/" || r.Header.Get("Origin") != "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+endpoint.Token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var request Request
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		response := handler(r.Context(), request)
		response.Host = info
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	})
	path := filepath.Join(dir, "endpoint.json")
	raw, err := json.Marshal(endpoint)
	if err == nil {
		err = securefs.WriteFileAtomic(path, raw)
	}
	if err != nil {
		_ = listener.Close()
		_ = lease.Release()
		return nil, err
	}
	registration := &Registration{server: server, lease: lease, path: path}
	go func() { _ = server.Serve(listener) }()
	return registration, nil
}

func (r *Registration) Close() {
	if r == nil {
		return
	}
	r.once.Do(func() { _ = r.server.Close(); _ = os.Remove(r.path); _ = r.lease.Release() })
}

func List(home string) ([]Endpoint, error) {
	root := filepath.Join(home, "run", "plugin-hosts")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var result []Endpoint
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "host-") {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		lease, acquired, err := storelock.TryAcquire(dir)
		if err != nil {
			return nil, err
		}
		if acquired {
			_ = lease.Release()
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, "endpoint.json"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var endpoint Endpoint
		if err := json.Unmarshal(raw, &endpoint); err != nil {
			return nil, err
		}
		result = append(result, endpoint)
	}
	return result, nil
}

func Call(ctx context.Context, endpoint Endpoint, request Request) (Response, error) {
	host, _, err := net.SplitHostPort(endpoint.Address)
	if err != nil || host != "127.0.0.1" || endpoint.Token == "" {
		return Response{}, errors.New("invalid local plugin host endpoint")
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return Response{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+endpoint.Address+"/", bytes.NewReader(raw))
	if err != nil {
		return Response{}, err
	}
	req.Header.Set("Authorization", "Bearer "+endpoint.Token)
	transport := &http.Transport{DialContext: (&net.Dialer{Timeout: time.Second}).DialContext, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("plugin host redirect denied") }}
	response, err := client.Do(req)
	if err != nil {
		return Response{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Response{}, fmt.Errorf("plugin host returned HTTP %d", response.StatusCode)
	}
	var result Response
	if err := json.NewDecoder(http.MaxBytesReader(nil, response.Body, 4*1024*1024)).Decode(&result); err != nil {
		return Response{}, err
	}
	if result.Host.ID != endpoint.Host.ID {
		return Response{}, errors.New("plugin host identity changed")
	}
	if result.Error != "" {
		return result, errors.New(result.Error)
	}
	return result, nil
}

func Inspect(ctx context.Context, home, id string) ([]Response, error) {
	endpoints, err := List(home)
	if err != nil {
		return nil, err
	}
	result := make([]Response, 0, len(endpoints))
	for _, endpoint := range endpoints {
		response, err := Call(ctx, endpoint, Request{Operation: "status", PluginID: id})
		if err != nil {
			response.Host = endpoint.Host
			response.Error = err.Error()
		}
		result = append(result, response)
	}
	return result, nil
}

package pluginhost

import (
	"errors"
	"sort"
)

// RendererActivationReport describes one phase of one transport activation.
// The host supplies renderer identity; activation is monotonic per renderer.
type RendererActivationReport struct {
	Activation uint64 `json:"activation"`
	Phase      string `json:"phase"`
	Status     string `json:"status"`
	Message    string `json:"message,omitempty"`
}

type RendererPhaseSnapshot struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type RendererClientSnapshot struct {
	RendererID   string                 `json:"renderer_id"`
	Activation   uint64                 `json:"activation"`
	Registration *RendererPhaseSnapshot `json:"registration,omitempty"`
	Render       *RendererPhaseSnapshot `json:"render,omitempty"`
	Cleanup      *RendererPhaseSnapshot `json:"cleanup,omitempty"`
}

type RendererActivationSnapshot struct {
	State   string                   `json:"state"`
	Clients []RendererClientSnapshot `json:"clients"`
}

type rendererRegistration struct {
	fingerprint string
	clients     map[string]RendererClientSnapshot
}

// RegisterRenderer adds the renderer transport to the same generation graph,
// including packages whose only contributions are UI slots.
func (r *ServiceRegistry) RegisterRenderer(module, fingerprint string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.renderers == nil {
		r.renderers = make(map[string]*rendererRegistration)
	}
	r.renderers[module] = &rendererRegistration{fingerprint: fingerprint, clients: make(map[string]RendererClientSnapshot)}
}

func (r *ServiceRegistry) ReportRenderer(module, fingerprint, renderer string, report RendererActivationReport) error {
	if renderer == "" || report.Activation == 0 || len(report.Message) > 4096 {
		return errors.New("invalid renderer activation report")
	}
	if report.Status != "active" && report.Status != "failed" && report.Status != "disposed" {
		return errors.New("invalid renderer activation status")
	}
	if report.Phase != "registration" && report.Phase != "render" && report.Phase != "cleanup" {
		return errors.New("invalid renderer activation phase")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.open {
		return errors.New("renderer graph is closed")
	}
	registered := r.renderers[module]
	if registered == nil || registered.fingerprint != fingerprint {
		return errors.New("renderer does not belong to this graph")
	}
	client := registered.clients[renderer]
	if report.Activation < client.Activation {
		return errors.New("stale renderer activation")
	}
	if report.Activation > client.Activation {
		if report.Phase != "registration" {
			return errors.New("renderer activation has not registered")
		}
		client = RendererClientSnapshot{RendererID: renderer, Activation: report.Activation}
	}
	if client.Cleanup != nil && client.Cleanup.Status == "disposed" && report.Phase != "cleanup" {
		return errors.New("renderer activation was disposed")
	}
	var phase **RendererPhaseSnapshot
	switch report.Phase {
	case "registration":
		phase = &client.Registration
	case "render":
		phase = &client.Render
	case "cleanup":
		phase = &client.Cleanup
	}
	// A successful registration or retry cannot erase a render/cleanup failure.
	if *phase == nil || (*phase).Status != "failed" {
		*phase = &RendererPhaseSnapshot{Status: report.Status, Message: report.Message}
	}
	registered.clients[renderer] = client
	return nil
}

func (r *ServiceRegistry) ForgetRenderer(renderer string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, registered := range r.renderers {
		delete(registered.clients, renderer)
	}
}

// Called under the registry read lock; snapshots own all nested values.
func (r *ServiceRegistry) rendererSnapshot(module string) *RendererActivationSnapshot {
	registered := r.renderers[module]
	if registered == nil {
		return nil
	}
	snapshot := &RendererActivationSnapshot{State: "pending", Clients: []RendererClientSnapshot{}}
	priority := map[string]int{"pending": 0, "disposed": 1, "registered": 2, "active": 3, "failed": 4}
	for _, client := range registered.clients {
		state := "pending"
		if client.Registration != nil && client.Registration.Status == "active" {
			state = "registered"
		}
		if client.Render != nil && client.Render.Status == "active" {
			state = "active"
		}
		if client.Cleanup != nil && client.Cleanup.Status == "disposed" {
			state = "disposed"
		}
		for _, phase := range []*RendererPhaseSnapshot{client.Registration, client.Render, client.Cleanup} {
			if phase != nil && phase.Status == "failed" {
				state = "failed"
			}
		}
		if priority[state] > priority[snapshot.State] {
			snapshot.State = state
		}
		clone := func(phase *RendererPhaseSnapshot) *RendererPhaseSnapshot {
			if phase == nil {
				return nil
			}
			copy := *phase
			return &copy
		}
		client.Registration, client.Render, client.Cleanup = clone(client.Registration), clone(client.Render), clone(client.Cleanup)
		snapshot.Clients = append(snapshot.Clients, client)
	}
	sort.Slice(snapshot.Clients, func(i, j int) bool { return snapshot.Clients[i].RendererID < snapshot.Clients[j].RendererID })
	return snapshot
}

package appserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// The same real appserver boundary serves native and independent SDK providers.
// The external case has no native artifacts provider or native payload directory.
// URLs alone, cross-session references, changed generations and disabled providers
// must fail; bounded ranges must not be replaced by a full-buffer read.
func TestThreadArtifactSelectedProvider(t *testing.T) {
	for _, kind := range []string{"native", "external"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			rt := newTestRuntime(t, &fakeClient{})
			home := t.TempDir()
			var selected pluginhost.Client
			if kind == "external" {
				fixture := os.Getenv("WUU_ARTIFACT_PROVIDER_FIXTURE")
				if fixture == "" {
					t.Skip("run node scripts/artifact-provider-acceptance.mjs for independent SDK fixture")
				}
				node, err := exec.LookPath("node")
				if err != nil {
					t.Fatal(err)
				}
				selected, err = pluginhost.Start(ctx, pluginhost.ProcessConfig{ID: "fixture.cloud-artifacts", Command: node, Args: []string{filepath.Join(fixture, "provider.ts")}, PluginRoot: fixture, ProjectRoot: rt.RootDir, WuuHome: home, Timeout: 5 * time.Second, PrepareOnly: true})
				if err != nil {
					t.Fatal(err)
				}
			} else {
				selected = runtime.NewArtifactService("fixture.native-artifacts", home, filepath.Join(home, "workspaces", "native"))
			}
			cfg := config.Default()
			cfg.DefaultProvider = rt.ProviderName
			cfg.Providers = map[string]config.ProviderConfig{rt.ProviderName: {Type: "openai-compatible", Model: rt.Model, BaseURL: "https://example.test/v1"}}
			attachTestModelGeneration(t, rt, selected, &threadDeleteServiceConsumer{}, config.NewMemoryService("fixture.artifact-config", cfg))
			state := testRuntimeState(t, rt)
			services, err := state.Services()
			if err != nil {
				t.Fatal(err)
			}
			content := []byte("%PDF-1.7\ncloud-only snapshot\n")
			artifact, err := services.Artifacts.Import(ctx, runtime.ArtifactScope{ThreadID: "source", PluginID: "fixture"}, pluginhost.ArtifactImportParams{Data: base64.StdEncoding.EncodeToString(content), Name: "report.pdf", MIMEType: "application/pdf"}, false)
			if err != nil {
				t.Fatal(err)
			}
			out := &lockedBuffer{}
			srv := New(rt, out, WithAccessPolicy(access.PolicyFunc(func(context.Context, access.Request) (access.Decision, error) {
				return access.Decision{Allow: true}, nil
			})))
			defer srv.Close()
			th := newThreadState("source", nil, "", "", rt.RootDir, false, time.Now())
			th.state = state.Retain()
			th.Turns = []Turn{{ID: "turn", Items: []ThreadItem{{ID: "item", Type: ThreadItemToolCall, ResultDetail: &toolresult.Result{Content: []toolresult.ContentPart{{Type: "file", URI: artifact.URI, Name: artifact.Name, MIMEType: artifact.MIMEType, Artifact: &toolresult.ArtifactPresentation{Ref: artifact.ID, SHA256: artifact.SHA256, SizeBytes: &artifact.Size}}}}}}}}
			srv.threads[th.ID] = th
			invoke := func(id, method string, params map[string]any) map[string]any {
				t.Helper()
				params["request_id"] = id
				raw, _ := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
				if err := srv.handleLine(ctx, raw); err != nil {
					t.Fatal(err)
				}
				deadline := time.Now().Add(5 * time.Second)
				for !strings.Contains(out.String(), `"id":"`+id+`"`) {
					if time.Now().After(deadline) {
						t.Fatalf("artifact response %s timed out", id)
					}
					time.Sleep(time.Millisecond)
				}
				return responseByID(t, parseOutput(t, out.String()), id)
			}
			params := map[string]any{"thread_id": "source", "uri": artifact.URI, "sha256": artifact.SHA256}
			resolved := invoke("resolve", "thread/artifact/resolve", params)
			if resolved["error"] != nil {
				t.Fatal(resolved)
			}
			metadata := remarshal[struct {
				Artifact     pluginhost.ArtifactImportResult `json:"artifact"`
				GenerationID string                          `json:"generation_id"`
			}](t, resolved["result"])
			if metadata.Artifact != artifact || metadata.GenerationID == "" {
				t.Fatalf("metadata=%+v", metadata)
			}
			params["generation_id"] = metadata.GenerationID
			params["offset"] = 9
			params["max_bytes"] = 10
			response := invoke("range", "thread/artifact/read", params)
			if response["error"] != nil {
				t.Fatal(response)
			}
			ranged := remarshal[runtime.ArtifactRangeResult](t, response["result"])
			if string(ranged.Data) != "cloud-only" || ranged.Total != int64(len(content)) {
				t.Fatalf("range=%+v", ranged)
			}
			params["generation_id"] = "retired-generation"
			if invoke("stale", "thread/artifact/read", params)["error"] == nil {
				t.Fatal("changed provider generation accepted")
			}
			params["generation_id"] = metadata.GenerationID
			params["uri"] = artifact.URI + "-unpublished"
			if invoke("unpublished", "thread/artifact/resolve", params)["error"] == nil {
				t.Fatal("URI-only access accepted")
			}
			params["uri"] = artifact.URI
			params["thread_id"] = "other"
			if invoke("other", "thread/artifact/resolve", params)["error"] == nil {
				t.Fatal("cross-session read accepted")
			}
			params["thread_id"] = "source"
			if kind == "external" {
				if strings.HasPrefix(artifact.URI, "wuu-artifact:") || len(artifact.ID) == 32 {
					t.Fatal("fixture accidentally used native identities")
				}
				if _, err := os.Stat(filepath.Join(home, "workspaces")); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("external bytes materialized locally")
				}
			}
			if kind == "external" {
				blocked, err := services.Artifacts.Import(ctx, runtime.ArtifactScope{ThreadID: "source", PluginID: "fixture"}, pluginhost.ArtifactImportParams{Data: base64.StdEncoding.EncodeToString(content), Name: "blocked.pdf", MIMEType: "application/pdf"}, false)
				if err != nil {
					t.Fatal(err)
				}
				th.mu.Lock()
				th.Turns[0].Items[0].ResultDetail.Content = append(th.Turns[0].Items[0].ResultDetail.Content, toolresult.ContentPart{Type: "file", URI: blocked.URI, Name: blocked.Name, MIMEType: blocked.MIMEType, Artifact: &toolresult.ArtifactPresentation{Ref: blocked.ID, SHA256: blocked.SHA256, SizeBytes: &blocked.Size}})
				th.mu.Unlock()
				alice, _ := principal.Authenticated(principal.Identity{Kind: "user", Issuer: "fixture", Tenant: "tenant", Subject: "alice"})
				bob, _ := principal.Authenticated(principal.Identity{Kind: "user", Issuer: "fixture", Tenant: "tenant", Subject: "bob"})
				aliceContext, bobContext := principal.WithContext(ctx, alice), principal.WithContext(ctx, bob)
				control := selected.(interface {
					InvokeService(context.Context, pluginhost.ServiceInvokeParams) (json.RawMessage, error)
				})
				wait, cancel := context.WithTimeout(ctx, 5*time.Second)
				defer cancel()
				started := func(count int) {
					t.Helper()
					raw, _ := json.Marshal(map[string]int{"count": count})
					if _, err := control.InvokeService(wait, pluginhost.ServiceInvokeParams{Service: "fixture.artifacts.control", Method: "wait-read", Params: raw}); err != nil {
						t.Fatal(err)
					}
				}
				cancellations := func(want int) {
					t.Helper()
					raw, err := control.InvokeService(wait, pluginhost.ServiceInvokeParams{Service: "fixture.artifacts.control", Method: "cancellations", Params: json.RawMessage(`{}`)})
					var result struct {
						Canceled int `json:"canceled"`
					}
					if err != nil || json.Unmarshal(raw, &result) != nil || result.Canceled != want {
						t.Fatalf("cancellations=%s err=%v want=%d", raw, err, want)
					}
				}
				dispatch := func(caller context.Context, id, method string, artifact pluginhost.ArtifactImportResult) error {
					raw, _ := json.Marshal(map[string]any{"id": id, "method": method, "params": map[string]any{"thread_id": "source", "uri": artifact.URI, "sha256": artifact.SHA256, "generation_id": metadata.GenerationID, "request_id": "same-caller-request-id", "offset": 0, "max_bytes": 1}})
					return srv.handleLine(caller, raw)
				}
				send := func(caller context.Context, id, method string, artifact pluginhost.ArtifactImportResult) {
					t.Helper()
					if err := dispatch(caller, id, method, artifact); err != nil {
						t.Fatal(err)
					}
				}
				settled := func(id string) {
					t.Helper()
					deadline := time.Now().Add(5 * time.Second)
					for !strings.Contains(out.String(), `"id":"`+id+`"`) {
						if time.Now().After(deadline) {
							t.Fatalf("%s did not settle", id)
						}
						time.Sleep(time.Millisecond)
					}
					if responseByID(t, parseOutput(t, out.String()), id)["error"] == nil {
						t.Fatal("canceled artifact delivered data")
					}
				}
				send(aliceContext, "blocked-alice", "thread/artifact/read", blocked)
				started(1)
				send(bobContext, "foreign-cancel", "thread/artifact/cancel", blocked)
				cancellations(0)
				send(bobContext, "blocked-bob", "thread/artifact/read", blocked)
				started(2)
				send(bobContext, "bob-cancel", "thread/artifact/cancel", blocked)
				settled("blocked-bob")
				cancellations(1)
				if strings.Contains(out.String(), `"id":"blocked-alice"`) {
					t.Fatal("one principal canceled another principal's range")
				}
				send(aliceContext, "alice-cancel", "thread/artifact/cancel", blocked)
				settled("blocked-alice")
				cancellations(2)

				metadataOnly, err := services.Artifacts.Import(ctx, runtime.ArtifactScope{ThreadID: "source", PluginID: "fixture"}, pluginhost.ArtifactImportParams{Data: base64.StdEncoding.EncodeToString(content), Name: "blocked-resolve.pdf", MIMEType: "application/pdf"}, false)
				if err != nil {
					t.Fatal(err)
				}
				th.mu.Lock()
				th.Turns[0].Items[0].ResultDetail.Content = append(th.Turns[0].Items[0].ResultDetail.Content, toolresult.ContentPart{Type: "file", URI: metadataOnly.URI, Name: metadataOnly.Name, MIMEType: metadataOnly.MIMEType, Artifact: &toolresult.ArtifactPresentation{Ref: metadataOnly.ID, SHA256: metadataOnly.SHA256, SizeBytes: &metadataOnly.Size}})
				th.mu.Unlock()
				resolveContext, cancelResolve := context.WithCancel(aliceContext)
				defer cancelResolve()
				returned := make(chan error, 1)
				go func() {
					returned <- dispatch(resolveContext, "blocked-resolve", "thread/artifact/resolve", metadataOnly)
				}()
				started(3)
				select {
				case err := <-returned:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(time.Second):
					cancelResolve()
					t.Fatal("metadata resolution blocked subsequent transport frames")
				}
				send(aliceContext, "resolve-cancel", "thread/artifact/cancel", metadataOnly)
				settled("blocked-resolve")
				cancellations(3)
			}
			outcome, found := rt.PluginHost.RetirePlugin(ctx, selected.ID(), errors.New("disabled"))
			if !found || outcome.Err != nil {
				t.Fatalf("retire=%+v found=%v", outcome, found)
			}
			if invoke("disabled", "thread/artifact/read", params)["error"] == nil {
				t.Fatal("disabled provider served a range")
			}
		})
	}
}

package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

type lateArtifactTool struct {
	registeredRuntimeToolClient
	started, finish chan struct{}
	path            string
}

func (c *lateArtifactTool) ExecuteTool(ctx context.Context, _ pluginhost.ToolExecuteParams) (pluginhost.ToolExecuteResult, error) {
	close(c.started)
	select {
	case <-c.finish:
	case <-ctx.Done():
		return pluginhost.ToolExecuteResult{}, ctx.Err()
	}
	return pluginhost.ToolExecuteResult{Result: toolresult.Result{Content: []toolresult.ContentPart{{Type: toolresult.ContentTypeFile, URI: c.path, Name: "late.txt", MIMEType: "text/plain"}}}}, nil
}

type artifactLifetimeConsumer struct{ generationClient }

func (*artifactLifetimeConsumer) ProvidedServices() []pluginhost.ServiceDescriptor { return nil }
func (*artifactLifetimeConsumer) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{{Name: ArtifactServiceName, MajorVersion: 1, Required: true}}
}

// Foreground execution outlives background retirement. Its late artifact must
// finish through the captured provider, while final generation release revokes it.
func TestArtifactMaterializationAfterBackgroundRetirement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	home, project := t.TempDir(), t.TempDir()
	path := filepath.Join(project, "late.txt")
	if err := os.WriteFile(path, []byte("late foreground result"), 0600); err != nil {
		t.Fatal(err)
	}
	tool := &lateArtifactTool{started: make(chan struct{}), finish: make(chan struct{}), path: path}
	artifacts := NewArtifactService(ArtifactModule, home, filepath.Join(home, "workspaces", "test"))
	consumer := &artifactLifetimeConsumer{generationClient: generationClient{id: StateServicesConsumer}}
	kernelConsumer := &artifactLifetimeConsumer{generationClient: generationClient{id: kernelHostModule}}
	clients := []pluginhost.Client{tool, artifacts, consumer, kernelConsumer}
	registry, conflicts := pluginhost.BuildServiceRegistry(clients...)
	if len(conflicts) != 0 {
		t.Fatal(conflicts)
	}
	host := pluginhost.New(clients...)
	host.AttachServiceRegistry(registry, conflicts)
	kernel := newKernelHostServices(nil, host)
	kernel.bindRegistry(registry)
	kernel.selectedServices = &runtimeServices{artifacts: NewArtifactClient(boundServiceCall(host, StateServicesConsumer, ArtifactServiceName))}
	host.SetToolResultMaterializer(kernel.materializeToolResult)
	if err := host.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	generation := &PluginGeneration{host: host}
	generation.retain()
	defer generation.Release()
	type completion struct {
		result toolresult.Result
		err    error
	}
	finished := make(chan completion, 1)
	go func() {
		result, err := host.ExecuteTool(ctx, host.ToolDefinitions()[0].Name, pluginhost.ToolExecuteInput{SessionID: "thread", ThreadID: "thread", TurnID: "turn", CallID: "late", CWD: project, Arguments: json.RawMessage(`{}`)})
		finished <- completion{result, err}
	}()
	select {
	case <-tool.started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	host.StopBackground()
	close(tool.finish)
	var result toolresult.Result
	select {
	case value := <-finished:
		if value.err != nil {
			t.Fatal(value.err)
		}
		result = value.result
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if len(result.Content) != 1 || !strings.HasPrefix(result.Content[0].URI, "wuu-artifact:") {
		t.Fatalf("result was not materialized: %+v", result)
	}
	uri := result.Content[0].URI
	data, _, err := kernel.selectedServices.artifacts.Read(ctx, "thread", uri, 1024)
	if err != nil || string(data) != "late foreground result" {
		t.Fatalf("captured artifact: %q %v", data, err)
	}
	generation.Release()
	if _, _, err := kernel.selectedServices.artifacts.Read(ctx, "thread", uri, 1024); err == nil {
		t.Fatal("released generation still served artifacts")
	}
}

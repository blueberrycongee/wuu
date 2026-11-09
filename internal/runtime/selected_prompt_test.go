package runtime

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/promptservice"
)

type externalPromptFixture struct{ *runtimeServiceConsumer }

func (*externalPromptFixture) ProvidedServices() []pluginhost.ServiceDescriptor {
	return promptservice.NewService().ProvidedServices()
}
func (*externalPromptFixture) InvokeService(context.Context, pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	return json.RawMessage(`{"content":"external complete prompt","sections":[]}`), nil
}
func TestSelectedPromptHasNoMandatoryNativeBase(t *testing.T) {
	ctx := context.Background()
	provider := &externalPromptFixture{&runtimeServiceConsumer{id: "external.prompt", state: pluginhost.StatePrepared}}
	consumer := &runtimeServiceConsumer{id: sessionExecutionConsumer, state: pluginhost.StatePrepared, requirements: []pluginhost.ServiceRequirement{{Name: promptservice.ServiceName, MajorVersion: 1, Required: true}}}
	registry, conflicts := pluginhost.BuildServiceRegistry(provider, consumer)
	host := pluginhost.New(provider, consumer)
	host.AttachServiceRegistry(registry, conflicts)
	defer host.Close(ctx)
	if err := host.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	assembler := agent.NewSystemPromptAssembler()
	assembler.Add(agent.NewStaticPromptSection("plugin", "unselected ordering", 1))
	result, err := assembleSelectedPrompt(ctx, host, promptservice.Request{Role: "main", CWD: "/workspace"}, assembler)
	if err != nil || result.Content != "external complete prompt" {
		t.Fatalf("selected prompt contaminated: %+v %v", result, err)
	}
	if err := host.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := assembleSelectedPrompt(ctx, host, promptservice.Request{Role: "main"}, nil); err == nil {
		t.Fatal("closed provider fell back to default prompt")
	}
}

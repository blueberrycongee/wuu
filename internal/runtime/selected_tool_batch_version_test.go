package runtime

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/toolbatch"
)

type versionedBatchFixture struct {
	*runtimeServiceConsumer
	descriptor pluginhost.ServiceDescriptor
	methods    []string
}

func (f *versionedBatchFixture) ProvidedServices() []pluginhost.ServiceDescriptor {
	return []pluginhost.ServiceDescriptor{f.descriptor}
}
func (f *versionedBatchFixture) InvokeService(ctx context.Context, p pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	f.methods = append(f.methods, p.Method)
	return json.RawMessage(`{"completed":true}`), nil
}

func TestSelectedToolBatchStreamingUsesSelectedMajor(t *testing.T) {
	for _, v1Streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "v1-buffer-v2-stream", true: "v1-stream-v2-buffer"}[v1Streaming], func(t *testing.T) {
			v1Descriptor, v2Descriptor := toolbatch.Descriptor(), toolbatch.StreamingDescriptor()
			if v1Streaming {
				v1Descriptor, v2Descriptor = v2Descriptor, v1Descriptor
			}
			v2Descriptor.Version = "2.0.0"
			v1 := &versionedBatchFixture{runtimeServiceConsumer: &runtimeServiceConsumer{id: "batch-v1", state: pluginhost.StatePrepared}, descriptor: v1Descriptor}
			v2 := &versionedBatchFixture{runtimeServiceConsumer: &runtimeServiceConsumer{id: "batch-v2", state: pluginhost.StatePrepared}, descriptor: v2Descriptor}
			consumer := &runtimeServiceConsumer{id: toolExecutionConsumer, state: pluginhost.StatePrepared, requirements: []pluginhost.ServiceRequirement{{Name: toolbatch.ServiceName, MajorVersion: 1, Required: true}}}
			registry, conflicts := pluginhost.BuildServiceRegistry(v1, v2, consumer)
			if len(conflicts) != 0 {
				t.Fatal(conflicts)
			}
			host := pluginhost.New(v1, v2, consumer)
			host.AttachServiceRegistry(registry, conflicts)
			defer host.Close(context.Background())
			if err := host.Activate(context.Background()); err != nil {
				t.Fatal(err)
			}
			selected := newSelectedToolBatch(host)
			if actual := toolbatch.SupportsStreaming(selected); actual != v1Streaming {
				t.Fatalf("selected @1 streaming=%t, want %t; @2 leaked into capability negotiation", actual, v1Streaming)
			}
			if err := selected.ExecuteBatch(context.Background(), toolbatch.Request{ID: "major-scoped"}, nil); err != nil {
				t.Fatal(err)
			}
			if len(v1.methods) != 1 || v1.methods[0] != "execute" || len(v2.methods) != 0 {
				t.Fatalf("dispatch routed to wrong major: v1=%v v2=%v", v1.methods, v2.methods)
			}
		})
	}
}

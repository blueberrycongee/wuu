package runtime

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRuntimePresentationRetainsGenerationAndReleasesOnce(t *testing.T) {
	client := &generationClient{id: "presentation"}
	generation := testPluginGeneration("presentation", client)
	rt := testGenerationSession(t, generation)
	rt.ProviderName = "provider"
	rt.ReadinessIssues = []ReadinessIssue{{Code: "initial"}}
	snapshot, release := rt.CaptureRuntimePresentation()
	require.Equal(t, "provider", snapshot.ProviderName)
	require.EqualValues(t, 2, generation.refs.Load())
	rt.ReadinessIssues[0].Code = "changed"
	require.Equal(t, "initial", snapshot.ReadinessIssues[0].Code)
	// Removing the session owner must not close the graph while a response uses it.
	rt.ReleasePluginGeneration(generation)
	require.False(t, client.closed)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); release() }()
	}
	wg.Wait()
	require.True(t, client.closed)
	require.EqualValues(t, -1, generation.refs.Load())
}

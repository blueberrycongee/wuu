package appserver

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/modelbudget"
	"github.com/stretchr/testify/require"
)

// Config reads run concurrently with real advanced-settings updates, as they
// do when the desktop polls state while another connection changes settings.
func TestConfigReadConcurrentAdvancedPublication(t *testing.T) {
	_, srv, out := publicationTestServer(t)
	ready, done := make(chan struct{}), make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = srv.handleLine(context.Background(), []byte(`{"id":"seed-read","method":"config/read"}`))
		close(ready)
		for i := 0; ; i++ {
			select {
			case <-done:
				return
			default:
				_ = srv.handleLine(context.Background(), []byte(fmt.Sprintf(`{"id":"read-%d","method":"config/read"}`, i)))
			}
		}
	}()
	<-ready
	var err error
	for i := 0; i < 50; i++ {
		v := "0.2"
		if i%2 == 0 {
			v = "0.8"
		}
		err = srv.handleLine(context.Background(), []byte(fmt.Sprintf(`{"id":"write-%d","method":"config/advanced/update","params":{"temperature":%s,"compact_threshold_pct":%s}}`, i, v, v)))
		if err != nil {
			break
		}
	}
	close(done)
	wg.Wait()
	require.NoError(t, err)
	responses := parseOutput(t, out.String())
	require.Nil(t, responseByID(t, responses, "seed-read")["error"])
	for i := 0; i < 50; i++ {
		require.Nil(t, responseByID(t, responses, fmt.Sprintf("write-%d", i))["error"])
	}
}

// Exercise the same publication used by the settings handler without the
// provider IO between writes, widening the window for the real read route.
func TestConfigReadConcurrentRuntimePublication(t *testing.T) {
	rt, srv, out := publicationTestServer(t)
	state, err := rt.RetainState()
	require.NoError(t, err)
	models, err := state.Models()
	require.NoError(t, err)
	require.NoError(t, state.Close())
	ready, done := make(chan struct{}), make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = srv.handleLine(context.Background(), []byte(`{"id":"ready","method":"config/read"}`))
		close(ready)
		for i := 0; ; i++ {
			select {
			case <-done:
				return
			default:
				_ = srv.handleLine(context.Background(), []byte(fmt.Sprintf(`{"id":"r-%d","method":"config/read"}`, i)))
			}
		}
	}()
	<-ready
	for i := 0; i < 5000; i++ {
		basis, captureErr := rt.CaptureAdvancedRuntime()
		if captureErr != nil {
			err = captureErr
			break
		}
		value := 0.2
		if i%2 == 0 {
			value = 0.8
		}
		_, err = rt.PublishAdvancedRuntime(basis, config.AgentConfig{Temperature: value, CompactThresholdPct: value}, models.Roles, modelbudget.Budget{ContextWindowTokens: 1000 + i}, modelbudget.Budget{})
		if err != nil {
			break
		}
	}
	close(done)
	wg.Wait()
	require.NoError(t, err)
	require.Nil(t, responseByID(t, parseOutput(t, out.String()), "ready")["error"])
}

func TestConfigReadConcurrentModelGenerationPublication(t *testing.T) {
	_, srv, out := publicationTestServer(t)
	ready, done := make(chan struct{}), make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = srv.handleLine(context.Background(), []byte(`{"id":"seed-read","method":"config/read"}`))
		close(ready)
		for i := 0; ; i++ {
			select {
			case <-done:
				return
			default:
				_ = srv.handleLine(context.Background(), []byte(fmt.Sprintf(`{"id":"read-%d","method":"config/read"}`, i)))
			}
		}
	}()
	<-ready
	var err error
	for i := 0; i < 12; i++ {
		mode := "read_only"
		if i%2 == 0 {
			mode = "unconfined"
		}
		err = srv.handleLine(context.Background(), []byte(fmt.Sprintf(`{"id":"model-%d","method":"config/model/update","params":{"model":"fake-model","permission_mode":%q}}`, i, mode)))
		if err != nil {
			break
		}
	}
	close(done)
	wg.Wait()
	require.NoError(t, err)
	responses := parseOutput(t, out.String())
	require.Nil(t, responseByID(t, responses, "seed-read")["error"])
	for i := 0; i < 12; i++ {
		require.Nil(t, responseByID(t, responses, fmt.Sprintf("model-%d", i))["error"])
	}
}

package acceptance_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/persistence"
	wuuruntime "github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/stretchr/testify/require"
)

// The same real native source must remain available to overlapping processes.
// Pipe handshakes coordinate observations and closure without timing sleeps.
func TestAcceptanceSharedNativeAuthorityProcess(t *testing.T) {
	if provider := os.Getenv("WUU_ACCEPTANCE_AUTHORITY_CHILD"); provider != "" {
		ctx := context.Background()
		home, root := os.Getenv("WUU_HOME"), os.Getenv("WUU_ACCEPTANCE_AUTHORITY_ROOT")
		modules := []string{"wuu.session-state", provider}
		opts := wuuruntime.StateOptions{RootDir: root, HomeDir: home, Config: config.Config{Composition: &composition.Config{Version: composition.Version, Modules: &modules, Services: []composition.Binding{{Service: persistence.ServiceName, Major: 1, Provider: provider}}}}}
		input, output := os.NewFile(3, "parent-input"), os.NewFile(4, "parent-output")
		defer input.Close()
		defer output.Close()
		decode, encode := json.NewDecoder(input), json.NewEncoder(output)
		state, err := wuuruntime.OpenState(ctx, opts)
		require.NoError(t, err)
		defer state.Close()
		record, found, err := state.Sessions.Find("shared-process-thread")
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, "parent initial", record.Title)
		_, err = state.Sessions.UpdateTitle(record.ID, "child wrote")
		require.NoError(t, err)
		require.NoError(t, encode.Encode("child-ready"))
		var message string
		require.NoError(t, decode.Decode(&message))
		require.Equal(t, "parent-wrote", message)
		record, found, err = state.Sessions.Find(record.ID)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, "parent wrote", record.Title)
		require.NoError(t, encode.Encode("parent-write-observed"))
		require.NoError(t, decode.Decode(&message))
		require.Equal(t, "parent-closed", message)
		_, err = state.Sessions.UpdateTitle(record.ID, "child survived parent closure")
		require.NoError(t, err)
		require.NoError(t, state.Close())
		require.NoError(t, encode.Encode("child-closed"))
		return
	}
	for _, provider := range []string{persistence.SQLiteModule, persistence.JSONLModule} {
		t.Run(provider, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			home, root := t.TempDir(), t.TempDir()
			t.Setenv("WUU_HOME", home)
			modules := []string{"wuu.session-state", provider}
			opts := wuuruntime.StateOptions{RootDir: root, HomeDir: home, Config: config.Config{Composition: &composition.Config{Version: composition.Version, Modules: &modules, Services: []composition.Binding{{Service: persistence.ServiceName, Major: 1, Provider: provider}}}}}
			first, err := wuuruntime.OpenState(ctx, opts)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, first.Close()) })
			_, err = first.Sessions.CreateInitialized(session.Session{ID: "shared-process-thread", CWD: root, Title: "parent initial"}, nil)
			require.NoError(t, err)
			childIn, parentOut, err := os.Pipe()
			require.NoError(t, err)
			parentIn, childOut, err := os.Pipe()
			require.NoError(t, err)
			defer parentIn.Close()
			defer parentOut.Close()
			binary, err := os.Executable()
			require.NoError(t, err)
			command := exec.CommandContext(ctx, binary, "-test.run=^TestAcceptanceSharedNativeAuthorityProcess$")
			command.Env = append(os.Environ(), "WUU_ACCEPTANCE_AUTHORITY_CHILD="+provider, "WUU_ACCEPTANCE_AUTHORITY_ROOT="+root)
			command.ExtraFiles = []*os.File{childIn, childOut}
			var logs bytes.Buffer
			command.Stdout, command.Stderr = &logs, &logs
			require.NoError(t, command.Start())
			childIn.Close()
			childOut.Close()
			completed := make(chan error, 1)
			go func() { completed <- command.Wait() }()
			defer func() {
				if t.Failed() {
					cancel()
				}
				err := <-completed
				cancel()
				require.NoError(t, err, "%s", logs.String())
			}()
			decode, encode := json.NewDecoder(parentIn), json.NewEncoder(parentOut)
			var message string
			require.NoError(t, decode.Decode(&message))
			require.Equal(t, "child-ready", message)
			record, found, err := first.Sessions.Find("shared-process-thread")
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, "child wrote", record.Title)
			_, err = first.Sessions.UpdateTitle(record.ID, "parent wrote")
			require.NoError(t, err)
			require.NoError(t, encode.Encode("parent-wrote"))
			require.NoError(t, decode.Decode(&message))
			require.Equal(t, "parent-write-observed", message)
			require.NoError(t, first.Close())
			require.NoError(t, encode.Encode("parent-closed"))
			require.NoError(t, decode.Decode(&message))
			require.Equal(t, "child-closed", message)
			reopened, err := wuuruntime.OpenState(ctx, opts)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, reopened.Close()) })
			record, found, err = reopened.Sessions.Find(record.ID)
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, "child survived parent closure", record.Title)
		})
	}
}

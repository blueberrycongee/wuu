package exec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/appserver"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/runtimeconfig"
	wuusdk "github.com/blueberrycongee/wuu/internal/sdk"
)

type localAppServerController struct {
	rt     *runtime.Session
	client *ProtocolClient
	cancel context.CancelFunc
	done   chan error
	pipes  []io.Closer

	sdkRuntime    *wuusdk.Runtime
	sdkClient     *wuusdk.Client
	sdkInit       wuusdk.Initialization
	selection     wuusdk.ModelSelection
	sdkSessions   map[string]*wuusdk.Session
	sdkRuns       map[string]*wuusdk.Run
	sdkEvents     *wuusdk.Subscription
	notifications chan Notification

	sdkShutdownMu   sync.Mutex
	sdkShutdownDone chan struct{}
	sdkShutdownErr  error
}

func NewLocalAppServerController(ctx context.Context, opts Options) (Controller, error) {
	embedded, err := wuusdk.New(wuusdk.Options{
		WorkDir:          opts.Workdir,
		ConfigPath:       opts.ConfigPath,
		IgnoreUserConfig: opts.IgnoreUserConfig,
		Provider:         opts.Provider,
		Model:            opts.Model,
		AgentProfile:     opts.AgentProfile,
		MaxTurns:         opts.MaxTurns,
		Effort:           opts.Effort,
		Variant:          opts.Variant,
		PermissionMode:   opts.PermissionMode,
		NoTools:          opts.NoTools,
		NonInteractive:   true,
	})
	if err != nil {
		return nil, err
	}
	client, err := embedded.Connect(ctx, wuusdk.ClientOptions{Name: "wuu-exec"})
	if err != nil {
		_ = embedded.Close()
		return nil, err
	}
	controller := &localAppServerController{
		sdkRuntime:  embedded,
		sdkClient:   client,
		sdkInit:     client.Initialization(),
		selection:   wuusdk.ModelSelection{Provider: opts.Provider, Model: opts.Model},
		sdkSessions: map[string]*wuusdk.Session{},
		sdkRuns:     map[string]*wuusdk.Run{},
		// The SDK owns the bounded pending queue. Avoid a second count-only
		// buffer here, which could retain hundreds of multi-megabyte deltas.
		notifications: make(chan Notification),
	}
	if value := strings.TrimSpace(opts.Variant); value != "" {
		controller.selection.Variant = &value
	}
	if value := strings.TrimSpace(opts.Effort); value != "" {
		controller.selection.Effort = &value
	}
	controller.sdkEvents = client.Subscribe(ctx, wuusdk.SubscriptionOptions{})
	go controller.bridgeSDKEvents()
	return controller, nil
}

// newLocalControllerForRuntime wires an in-process app server (over pipes)
// around an already-built runtime.Session. NewLocalAppServerController builds
// the runtime from config; end-to-end tests can supply a fake-provider runtime.
func newLocalControllerForRuntime(ctx context.Context, rt *runtime.Session) *localAppServerController {
	serverInR, serverInW := io.Pipe()
	serverOutR, serverOutW := io.Pipe()
	serverCtx, cancel := context.WithCancel(ctx)
	controller := &localAppServerController{
		rt:     rt,
		client: NewProtocolClient(serverOutR, serverInW),
		cancel: cancel,
		done:   make(chan error, 1),
		pipes:  []io.Closer{serverInR, serverInW, serverOutR, serverOutW},
	}
	go func() {
		err := appserver.RunStdio(serverCtx, rt, serverInR, serverOutW)
		_ = serverInR.CloseWithError(err)
		_ = serverOutW.CloseWithError(err)
		controller.done <- err
	}()
	return controller
}

func loadExecConfig(rootDir, homeDir string, opts Options) (config.Config, string, error) {
	cfg, configPath, _, err := runtimeconfig.Load(runtimeconfig.LoadOptions{
		RootDir:          rootDir,
		HomeDir:          homeDir,
		ConfigPath:       opts.ConfigPath,
		IgnoreUserConfig: opts.IgnoreUserConfig,
	})
	return cfg, configPath, err
}

func (c *localAppServerController) Initialize(ctx context.Context) (appserver.InitializeResult, error) {
	if c.sdkClient != nil {
		var result appserver.InitializeResult
		err := json.Unmarshal(c.sdkInit.Raw, &result)
		return result, err
	}
	var result appserver.InitializeResult
	err := c.client.Call(ctx, appserver.MethodInitialize, nil, &result)
	return result, err
}

func (c *localAppServerController) StartThread(ctx context.Context, ephemeral bool) (appserver.Thread, error) {
	if c.sdkClient != nil {
		session, err := c.sdkClient.NewSession(ctx, wuusdk.SessionOptions{Ephemeral: ephemeral})
		return c.rememberSDKSession(session, err)
	}
	var result appserver.ThreadStartResult
	params := appserver.ThreadStartParams{Ephemeral: ephemeral}
	err := c.client.Call(ctx, appserver.MethodThreadStart, params, &result)
	return result.Thread, err
}

func (c *localAppServerController) ResumeThread(ctx context.Context, threadID string) (appserver.Thread, error) {
	if c.sdkClient != nil {
		session, err := c.sdkClient.ResumeSession(ctx, threadID)
		if err == nil {
			err = session.SelectModel(ctx, c.selection)
		}
		return c.rememberSDKSession(session, err)
	}
	var result appserver.ThreadResumeResult
	params := appserver.ThreadResumeParams{SessionID: strings.TrimSpace(threadID)}
	err := c.client.Call(ctx, appserver.MethodThreadResume, params, &result)
	return result.Thread, err
}

func (c *localAppServerController) ForkThread(ctx context.Context, threadID string) (appserver.Thread, error) {
	if c.sdkClient != nil {
		session, err := c.sdkClient.ForkSession(ctx, threadID)
		if err == nil {
			err = session.SelectModel(ctx, c.selection)
		}
		return c.rememberSDKSession(session, err)
	}
	var result appserver.ThreadForkResult
	params := appserver.ThreadForkParams{ThreadID: strings.TrimSpace(threadID)}
	err := c.client.Call(ctx, appserver.MethodThreadFork, params, &result)
	return result.Thread, err
}

func (c *localAppServerController) StartRun(ctx context.Context, params appserver.RunStartParams) (appserver.Run, error) {
	if c.sdkClient != nil {
		session := c.sdkSessions[strings.TrimSpace(params.ThreadID)]
		if session == nil {
			return appserver.Run{}, fmt.Errorf("session %q is not acquired", strings.TrimSpace(params.ThreadID))
		}
		images := make([]wuusdk.Image, 0, len(params.Images))
		for _, image := range params.Images {
			images = append(images, wuusdk.Image{MediaType: image.MediaType, Data: image.Data, Original: image.Original})
		}
		files := make([]wuusdk.File, 0, len(params.Files))
		for _, file := range params.Files {
			files = append(files, wuusdk.File{MediaType: file.MediaType, Data: file.Data, Filename: file.Filename})
		}
		permissionMode := ""
		if params.PermissionMode != nil {
			permissionMode = *params.PermissionMode
		}
		run, err := session.Send(ctx, wuusdk.SendOptions{
			Prompt:         params.Prompt,
			Images:         images,
			Files:          files,
			PermissionMode: permissionMode,
			OutputSchema:   params.OutputSchema,
			Provider:       params.Request.Requested.Provider,
			Model:          params.Request.Requested.Model,
			Variant:        params.Request.Requested.Variant,
			Effort:         params.Request.Requested.Effort,
			AgentProfile:   params.Request.AgentProfile,
			MaxTurns:       params.Request.MaxTurns,
			Timeout:        time.Duration(params.Request.TimeoutMS) * time.Millisecond,
			NoTools:        params.Request.NoTools,
		})
		if err != nil {
			return appserver.Run{}, fromSDKError(err)
		}
		c.sdkRuns[run.ID()] = run
		snapshot, ok := run.Snapshot()
		if !ok {
			return appserver.Run{}, errors.New("started run has no snapshot")
		}
		var result appserver.Run
		err = json.Unmarshal(snapshot.Raw, &result)
		return result, err
	}
	var result appserver.RunStartResult
	err := c.client.Call(ctx, appserver.MethodRunStart, params, &result)
	return result.Run, err
}

func (c *localAppServerController) InterruptRun(ctx context.Context, runID, reason string) (appserver.Run, error) {
	if c.sdkClient != nil {
		run := c.sdkRuns[strings.TrimSpace(runID)]
		if run == nil {
			return appserver.Run{}, fmt.Errorf("run %q is not owned by this controller", strings.TrimSpace(runID))
		}
		snapshot, err := run.Cancel(ctx, reason)
		if err != nil {
			return appserver.Run{}, fromSDKError(err)
		}
		var result appserver.Run
		err = json.Unmarshal(snapshot.Raw, &result)
		return result, err
	}
	var result appserver.RunInterruptResult
	err := c.client.Call(ctx, appserver.MethodRunInterrupt, appserver.RunInterruptParams{RunID: strings.TrimSpace(runID), Reason: strings.TrimSpace(reason)}, &result)
	return result.Run, err
}

// shutdownFallbackTimeout bounds Shutdown when the caller's context carries
// no deadline, so a wedged turn can never hang shutdown forever.
const shutdownFallbackTimeout = 10 * time.Second

func (c *localAppServerController) Shutdown(ctx context.Context) error {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, shutdownFallbackTimeout)
		defer cancel()
	}
	if c.sdkClient != nil {
		done := c.startSDKShutdown()
		select {
		case <-done:
			c.sdkShutdownMu.Lock()
			err := c.sdkShutdownErr
			c.sdkShutdownMu.Unlock()
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if c.cancel != nil {
		defer c.cancel()
	}
	// The notification channel is drained until the protocol closes. Without
	// this a full buffer blocks the read loop, which would then never read
	// the shutdown response.
	drainDone := make(chan struct{})
	go func() {
		for range c.client.Notifications() {
		}
		close(drainDone)
	}()
	var result appserver.OKResult
	err := c.client.Call(ctx, appserver.MethodShutdown, nil, &result)
	for _, pipe := range c.pipes {
		_ = pipe.Close()
	}
	if c.done != nil {
		select {
		case runErr := <-c.done:
			if err == nil && runErr != nil && !errors.Is(runErr, io.ErrClosedPipe) {
				err = runErr
			}
		case <-ctx.Done():
			// The run loop has not finished draining its turns and worker
			// finalizers; closing the shared runtime under it would race, so
			// surface the timeout instead of cleaning up.
			return errors.Join(err, fmt.Errorf("app server run loop did not exit before shutdown deadline: %w", ctx.Err()))
		}
	}
	// The pipes are closed, so the protocol read loop has closed the
	// notification channel and the drain goroutine is done or about to be.
	<-drainDone
	// RunStdio synchronously drains app-server-owned turns and worker
	// finalizers. Only then is it safe to close the shared runtime resources
	// those terminal paths still use.
	if c.rt != nil {
		_, _ = c.rt.Cleanup()
	}
	return err
}

func (c *localAppServerController) startSDKShutdown() <-chan struct{} {
	c.sdkShutdownMu.Lock()
	defer c.sdkShutdownMu.Unlock()
	if c.sdkShutdownDone == nil {
		c.sdkShutdownDone = make(chan struct{})
		go c.finishSDKShutdown()
	}
	return c.sdkShutdownDone
}

func (c *localAppServerController) finishSDKShutdown() {
	// Draining keeps the event bridge from blocking while Client.Close cancels
	// its subscription and waits for the app-server connection to finish.
	drainDone := make(chan struct{})
	go func() {
		for range c.notifications {
		}
		close(drainDone)
	}()

	clientErr := c.sdkClient.Close(context.Background())
	<-drainDone
	var runtimeErr error
	if c.sdkRuntime != nil {
		runtimeErr = c.sdkRuntime.Close()
	}

	c.sdkShutdownMu.Lock()
	c.sdkShutdownErr = errors.Join(clientErr, runtimeErr)
	close(c.sdkShutdownDone)
	c.sdkShutdownMu.Unlock()
}

func (c *localAppServerController) Notifications() <-chan Notification {
	if c.sdkClient != nil {
		return c.notifications
	}
	return c.client.Notifications()
}

func (c *localAppServerController) rememberSDKSession(session *wuusdk.Session, err error) (appserver.Thread, error) {
	if err != nil {
		return appserver.Thread{}, fromSDKError(err)
	}
	snapshot, ok := session.Snapshot()
	if !ok {
		return appserver.Thread{}, errors.New("acquired session has no snapshot")
	}
	var thread appserver.Thread
	if err := json.Unmarshal(snapshot.Raw, &thread); err != nil {
		return appserver.Thread{}, err
	}
	c.sdkSessions[session.ID()] = session
	return thread, nil
}

func fromSDKError(err error) error {
	var protocolErr *wuusdk.ProtocolError
	if errors.As(err, &protocolErr) {
		return &ProtocolError{Code: protocolErr.Code, Message: protocolErr.Message}
	}
	return err
}

func (c *localAppServerController) bridgeSDKEvents() {
	defer close(c.notifications)
	for event := range c.sdkEvents.Events {
		c.notifications <- Notification{Method: event.Method, Params: append(json.RawMessage(nil), event.Params...)}
	}
	if err := c.sdkEvents.Err(); err != nil {
		c.notifications <- Notification{Err: err}
	}
}

func resolveWorkdir(input string) (string, error) {
	if strings.TrimSpace(input) == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("get current directory: %w", err)
		}
		return cwd, nil
	}
	abs, err := filepath.Abs(input)
	if err != nil {
		return "", fmt.Errorf("resolve workdir: %w", err)
	}
	return abs, nil
}

func applyConfigOverrides(cfg *config.Config, opts Options) error {
	return runtimeconfig.ApplyOverrides(cfg, runtimeconfig.Overrides{
		AgentProfile:   opts.AgentProfile,
		MaxTurns:       opts.MaxTurns,
		Effort:         opts.Effort,
		Variant:        opts.Variant,
		PermissionMode: opts.PermissionMode,
	})
}

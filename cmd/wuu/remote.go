package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/blueberrycongee/wuu/internal/appserver"
	"github.com/blueberrycongee/wuu/internal/remote/host"
	"github.com/blueberrycongee/wuu/internal/remote/phone"
	"github.com/blueberrycongee/wuu/internal/remote/secure"
	"github.com/blueberrycongee/wuu/internal/remote/server"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

// remoteStorePath is remote.json inside the unified wuu home: the host's
// long-term identity plus its paired devices, next to auth.json.
func remoteStorePath() (string, error) {
	home, err := statepath.Home(os.Getenv("HOME"))
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "remote.json"), nil
}

func defaultPhoneStorePath() (string, error) {
	home, err := statepath.Home(os.Getenv("HOME"))
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "phone.json"), nil
}

func runRelay(args []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return server.Run(ctx, args, os.Stdout)
}

func runRemote(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: wuu remote <init|host|status|devices|phone> ...")
	}
	switch args[0] {
	case "account":
		return runRemoteAccount(args[1:])
	case "init":
		return runRemoteInit(args[1:])
	case "host":
		return runRemoteHost(args[1:])
	case "status":
		return runRemoteStatus(args[1:])
	case "devices":
		return runRemoteDevices(args[1:])
	case "phone":
		return runRemotePhone(args[1:])
	default:
		return fmt.Errorf("unknown remote subcommand %q", args[0])
	}
}

// runRemoteInit creates (or updates) the host's remote identity and relay
// configuration.
func runRemoteInit(args []string) error {
	fs := flag.NewFlagSet("remote init", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	relayURL := fs.String("relay", "", "relay websocket url (ws[s]://host:port/v1/connect)")
	name := fs.String("name", "", "host display name (default: hostname)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	path, err := remoteStorePath()
	if err != nil {
		return err
	}
	hostName := strings.TrimSpace(*name)
	if hostName == "" {
		if hn, err := os.Hostname(); err == nil {
			hostName = hn
		}
	}
	store, err := host.LoadOrCreateStore(path, hostName)
	if err != nil {
		return err
	}
	if strings.TrimSpace(*relayURL) != "" {
		if err := store.SetRelayURL(strings.TrimSpace(*relayURL)); err != nil {
			return err
		}
	}
	if strings.TrimSpace(*name) != "" {
		if err := store.SetHostName(hostName); err != nil {
			return err
		}
	}
	fmt.Printf("remote identity: %s\n", secure.Fingerprint(store.Identity().Public()))
	fmt.Printf("store:           %s\n", path)
	if store.RelayURL() == "" {
		fmt.Println("relay:           (unset; run wuu remote init --relay ws://...)")
	} else {
		fmt.Printf("relay:           %s\n", store.RelayURL())
	}
	return nil
}

// runRemoteHost runs the host daemon: it builds the same runtime as
// `wuu app-server` for the chosen workdir and serves paired phones through
// the relay.
func runRemoteHost(args []string) error {
	fs := flag.NewFlagSet("remote host", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	providerName := fs.String("provider", "", "provider name in config")
	modelOverride := fs.String("model", "", "model override")
	workdir := fs.String("workdir", "", "workspace directory")
	workspaceID := fs.String("workspace-id", "", "stable workspace identity")
	relayURL := fs.String("relay", "", "relay url override")
	pair := fs.Bool("pair", false, "open a pairing window and print the pairing URI")
	pairTimeout := fs.Duration("pair-timeout", 10*time.Minute, "pairing window duration")
	pairOnce := fs.Bool("pair-once", true, "close the pairing window after the first phone pairs")
	if err := fs.Parse(args); err != nil {
		return err
	}

	storePath, err := remoteStorePath()
	if err != nil {
		return err
	}
	store, err := host.LoadOrCreateStore(storePath, "")
	if err != nil {
		return err
	}

	rootDir, err := resolveWorkdir(*workdir)
	if err != nil {
		return err
	}
	var rt *runtime.Session
	opts := host.Options{
		Store:    store,
		RelayURL: strings.TrimSpace(*relayURL),
		Workdir:  rootDir,
		Logf:     func(format string, a ...any) { fmt.Fprintf(os.Stderr, format+"\n", a...) },
	}
	if address := os.Getenv("WUU_DESKTOP_APP_SERVER_ADDR"); address != "" || os.Getenv("WUU_DESKTOP_APP_SERVER_TOKEN") != "" {
		opts.AppServer, err = host.DesktopAppServer(address, os.Getenv("WUU_DESKTOP_APP_SERVER_TOKEN"))
		if err != nil {
			return err
		}
	} else {
		homeDir := os.Getenv("HOME")
		cfg, configPath, err := loadOrCreateAppServerConfig(rootDir, homeDir)
		if err != nil {
			return err
		}
		rt, err = runtime.NewSession(runtime.Options{
			RootDir:       rootDir,
			WorkspaceID:   *workspaceID,
			HomeDir:       homeDir,
			ConfigPath:    configPath,
			Config:        cfg,
			ProviderName:  *providerName,
			ModelOverride: *modelOverride,
		})
		if err != nil {
			return err
		}
		defer func() { _, _ = rt.Cleanup() }()

		opts.Runtime = rt
	}
	if *pair {
		opts.Pairing = &host.PairingConfig{
			Timeout: *pairTimeout,
			Once:    *pairOnce,
			OnURI: func(uri string) {
				fmt.Printf("pairing uri (scan or pass to `wuu remote phone pair --uri`):\n%s\n", uri)
			},
			OnPaired: func(name string, pub []byte) {
				fmt.Printf("paired: %s (%s)\n", name, secure.Fingerprint(pub))
			},
		}
	}
	h, err := host.New(opts)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Printf("remote host %s serving %s via %s\n",
		secure.Fingerprint(store.Identity().Public()), rootDir, firstNonEmpty(strings.TrimSpace(*relayURL), store.RelayURL()))
	err = h.Run(ctx)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// remoteDeviceJSON is the machine-readable form of one paired device, shared
// by `remote devices --json` and `remote status --json`.
type remoteDeviceJSON struct {
	Pub         string `json:"pub"`
	Fingerprint string `json:"fingerprint"`
	Name        string `json:"name,omitempty"`
	AddedAt     string `json:"added_at"`
}

func remoteDeviceFingerprint(pub string) string {
	raw, err := secure.DecodeKey(pub)
	if err != nil {
		return pub
	}
	return secure.Fingerprint(raw)
}

func remoteDevicesJSON(store *host.Store) []remoteDeviceJSON {
	devices := store.Devices()
	out := make([]remoteDeviceJSON, 0, len(devices))
	for _, d := range devices {
		out = append(out, remoteDeviceJSON{
			Pub:         d.Pub,
			Fingerprint: remoteDeviceFingerprint(d.Pub),
			Name:        d.Name,
			AddedAt:     d.AddedAt.Format(time.RFC3339),
		})
	}
	return out
}

// runRemoteStatus reports the host-side remote configuration in one shot;
// --json is what shells (desktop settings panel) consume.
func runRemoteStatus(args []string) error {
	fs := flag.NewFlagSet("remote status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	path, err := remoteStorePath()
	if err != nil {
		return err
	}
	store, err := host.LoadOrCreateStore(path, "")
	if err != nil {
		return err
	}
	if *asJSON {
		out := struct {
			AccountServer string             `json:"account_server,omitempty"`
			Fingerprint   string             `json:"fingerprint"`
			HostName      string             `json:"host_name,omitempty"`
			RelayURL      string             `json:"relay_url,omitempty"`
			Store         string             `json:"store"`
			Devices       []remoteDeviceJSON `json:"devices"`
		}{
			AccountServer: func() string {
				if c := store.Account(); c != nil {
					return c.Server
				}
				return ""
			}(),
			Fingerprint: secure.Fingerprint(store.Identity().Public()),
			HostName:    store.HostName(),
			RelayURL:    store.RelayURL(),
			Store:       path,
			Devices:     remoteDevicesJSON(store),
		}
		data, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	}
	fmt.Printf("remote identity: %s\n", secure.Fingerprint(store.Identity().Public()))
	fmt.Printf("store:           %s\n", path)
	if store.RelayURL() == "" {
		fmt.Println("relay:           (unset; run wuu remote init --relay ws://...)")
	} else {
		fmt.Printf("relay:           %s\n", store.RelayURL())
	}
	fmt.Printf("devices:         %d paired\n", len(store.Devices()))
	return nil
}

func runRemoteDevices(args []string) error {
	if len(args) > 0 && args[0] == "remove" {
		return runRemoteDevicesRemove(args[1:])
	}
	fs := flag.NewFlagSet("remote devices", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	path, err := remoteStorePath()
	if err != nil {
		return err
	}
	store, err := host.LoadOrCreateStore(path, "")
	if err != nil {
		return err
	}
	if *asJSON {
		data, err := json.MarshalIndent(remoteDevicesJSON(store), "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	}
	devices := store.Devices()
	if len(devices) == 0 {
		fmt.Println("no paired devices")
		return nil
	}
	for _, d := range devices {
		fmt.Printf("%s  %s  paired %s\n", remoteDeviceFingerprint(d.Pub), d.Name, d.AddedAt.Format(time.RFC3339))
	}
	return nil
}

// runRemoteDevicesRemove revokes a paired phone by fingerprint or full key.
// The running host process keeps its own in-memory store: revocation takes
// effect for new handshakes after that process reloads or restarts, which the
// desktop shell does automatically when it manages the host.
func runRemoteDevicesRemove(args []string) error {
	fs := flag.NewFlagSet("remote devices remove", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: wuu remote devices remove <fingerprint|pub>")
	}
	target := strings.TrimSpace(fs.Arg(0))
	path, err := remoteStorePath()
	if err != nil {
		return err
	}
	store, err := host.LoadOrCreateStore(path, "")
	if err != nil {
		return err
	}
	var matches []host.StoredDevice
	for _, d := range store.Devices() {
		if d.Pub == target || remoteDeviceFingerprint(d.Pub) == target {
			matches = append(matches, d)
		}
	}
	switch len(matches) {
	case 0:
		return fmt.Errorf("no paired device matches %q", target)
	case 1:
		if err := store.RemoveDevice(matches[0].Pub); err != nil {
			return err
		}
		fmt.Printf("removed: %s (%s)\n", matches[0].Name, remoteDeviceFingerprint(matches[0].Pub))
		return nil
	default:
		return fmt.Errorf("%d devices match %q; pass the full key", len(matches), target)
	}
}

// runRemotePhone is the development phone: it exercises the exact protocol a
// mobile app will speak (pair, attach, drive the app-server end to end).
func runRemotePhone(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: wuu remote phone <pair|status|send|watch> ...")
	}
	switch args[0] {
	case "pair":
		return runRemotePhonePair(args[1:])
	case "status":
		return runRemotePhoneStatus(args[1:])
	case "send":
		return runRemotePhoneSend(args[1:])
	case "watch":
		return runRemotePhoneWatch(args[1:])
	default:
		return fmt.Errorf("unknown remote phone subcommand %q", args[0])
	}
}

func phoneStoreFlag(fs *flag.FlagSet) *string {
	return fs.String("store", "", "credential file (default <wuu home>/phone.json)")
}

func resolvePhoneStore(flagValue string) (string, error) {
	if strings.TrimSpace(flagValue) != "" {
		return strings.TrimSpace(flagValue), nil
	}
	return defaultPhoneStorePath()
}

func runRemotePhonePair(args []string) error {
	fs := flag.NewFlagSet("remote phone pair", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	uri := fs.String("uri", "", "pairing uri from the host (wuu://pair?...)")
	name := fs.String("name", "dev-phone", "device display name")
	storeFlag := phoneStoreFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*uri) == "" {
		return errors.New("--uri is required")
	}
	storePath, err := resolvePhoneStore(*storeFlag)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	creds, err := phone.Pair(ctx, strings.TrimSpace(*uri), strings.TrimSpace(*name))
	if err != nil {
		return err
	}
	if err := creds.Save(storePath); err != nil {
		return err
	}
	hostPub, _ := secure.DecodeKey(creds.HostPub)
	fmt.Printf("paired with host %q (%s)\n", creds.HostName, secure.Fingerprint(hostPub))
	fmt.Printf("credentials: %s\n", storePath)
	return nil
}

// connectPhone spins up a client and waits for the first attach.
func connectPhone(ctx context.Context, storePath string) (*phone.Client, func(), error) {
	creds, err := phone.LoadCredentials(storePath)
	if err != nil {
		return nil, nil, fmt.Errorf("load credentials (pair first?): %w", err)
	}
	client, err := phone.NewClient(creds, phone.Options{})
	if err != nil {
		return nil, nil, err
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = client.Run(runCtx)
	}()
	stop := func() {
		cancel()
		<-done
	}
	waitCtx, waitCancel := context.WithTimeout(ctx, 30*time.Second)
	defer waitCancel()
	if err := client.WaitAttached(waitCtx); err != nil {
		stop()
		return nil, nil, fmt.Errorf("attach to host: %w (is `wuu remote host` running?)", err)
	}
	return client, stop, nil
}

func runRemotePhoneStatus(args []string) error {
	fs := flag.NewFlagSet("remote phone status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	storeFlag := phoneStoreFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	storePath, err := resolvePhoneStore(*storeFlag)
	if err != nil {
		return err
	}
	ctx := context.Background()
	client, stop, err := connectPhone(ctx, storePath)
	if err != nil {
		return err
	}
	defer stop()

	proto := client.Protocol()
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var init appserver.InitializeResult
	if err := proto.Call(callCtx, appserver.MethodInitialize, nil, &init); err != nil {
		return err
	}
	if st, ok := client.LatestState(); ok {
		fmt.Printf("host:     %s (wuu %s)\n", st.Host.Name, st.Host.Version)
		fmt.Printf("workdir:  %s\n", st.Host.Workdir)
		fmt.Printf("model:    %s/%s\n", st.Host.Provider, st.Host.Model)
		fmt.Printf("running:  %d turn(s)\n", len(st.Running))
	}
	var threads appserver.ThreadListResult
	if err := proto.Call(callCtx, appserver.MethodThreadList, appserver.ThreadListParams{}, &threads); err != nil {
		return err
	}
	fmt.Printf("threads:  %d\n", len(threads.Threads))
	for i, th := range threads.Threads {
		if i >= 10 {
			fmt.Printf("  ... and %d more\n", len(threads.Threads)-10)
			break
		}
		title := th.Title
		if title == "" {
			title = "(untitled)"
		}
		fmt.Printf("  %s  %s\n", th.ID, title)
	}
	return nil
}

func runRemotePhoneSend(args []string) error {
	fs := flag.NewFlagSet("remote phone send", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	storeFlag := phoneStoreFlag(fs)
	threadID := fs.String("thread", "", "existing thread id (default: start a new thread)")
	prompt := fs.String("prompt", "", "user message to send")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*prompt) == "" && fs.NArg() > 0 {
		*prompt = strings.Join(fs.Args(), " ")
	}
	if strings.TrimSpace(*prompt) == "" {
		return errors.New("--prompt is required")
	}
	storePath, err := resolvePhoneStore(*storeFlag)
	if err != nil {
		return err
	}
	ctx := context.Background()
	client, stop, err := connectPhone(ctx, storePath)
	if err != nil {
		return err
	}
	defer stop()
	proto := client.Protocol()

	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var init appserver.InitializeResult
	if err := proto.Call(callCtx, appserver.MethodInitialize, nil, &init); err != nil {
		return err
	}
	tid := strings.TrimSpace(*threadID)
	if tid == "" {
		var started appserver.ThreadStartResult
		if err := proto.Call(callCtx, appserver.MethodThreadStart, appserver.ThreadStartParams{}, &started); err != nil {
			return err
		}
		tid = started.Thread.ID
		fmt.Printf("thread: %s\n", tid)
	} else {
		var resumed appserver.ThreadResumeResult
		if err := proto.Call(callCtx, appserver.MethodThreadResume, appserver.ThreadResumeParams{SessionID: tid}, &resumed); err != nil {
			return err
		}
	}

	var turn appserver.TurnStartResult
	if err := proto.Call(callCtx, appserver.MethodTurnStart, appserver.TurnStartParams{ThreadID: tid, Prompt: *prompt}, &turn); err != nil {
		return err
	}
	fmt.Printf("turn started: %s\n", turn.Turn.ID)

	// Stream notifications until the turn finishes. Deliberately no
	// timeout: remote turns run as long as they run.
	for note := range proto.Notifications() {
		switch note.Method {
		case appserver.NotificationAgentMessageDelta:
			var delta struct {
				Delta string `json:"delta"`
			}
			_ = json.Unmarshal(note.Params, &delta)
			fmt.Print(delta.Delta)
		case appserver.NotificationTurnCompleted:
			var done appserver.TurnCompletedNotification
			_ = json.Unmarshal(note.Params, &done)
			if done.ThreadID == tid {
				fmt.Printf("\nturn completed (%d in / %d out tokens)\n", done.InputTokens, done.OutputTokens)
				return nil
			}
		case appserver.NotificationTurnError:
			var terr appserver.TurnErrorNotification
			_ = json.Unmarshal(note.Params, &terr)
			if terr.ThreadID == tid {
				return fmt.Errorf("turn error: %s", terr.Error)
			}
		}
	}
	return errors.New("connection closed before turn completed")
}

func runRemotePhoneWatch(args []string) error {
	fs := flag.NewFlagSet("remote phone watch", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	storeFlag := phoneStoreFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	storePath, err := resolvePhoneStore(*storeFlag)
	if err != nil {
		return err
	}
	ctx, stopSig := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSig()
	client, stop, err := connectPhone(ctx, storePath)
	if err != nil {
		return err
	}
	defer stop()

	if st, ok := client.LatestState(); ok {
		printHostState(st)
	}
	proto := client.Protocol()
	fmt.Println("watching host (ctrl-c to stop)")
	for {
		select {
		case <-ctx.Done():
			return nil
		case st := <-client.States():
			printHostState(st)
		case note, ok := <-proto.Notifications():
			if !ok {
				// The protocol client died (fresh attach after a resume
				// failure); grab the replacement.
				if err := client.WaitAttached(ctx); err != nil {
					return nil
				}
				proto = client.Protocol()
				continue
			}
			fmt.Printf("event %s %s\n", note.Method, truncateJSON(note.Params, 160))
		}
	}
}

func printHostState(st phone.HostState) {
	fmt.Printf("state v%d: host=%s workdir=%s running=%d\n", st.Ver, st.Host.Name, st.Host.Workdir, len(st.Running))
	for _, r := range st.Running {
		fmt.Printf("  running thread=%s turn=%s since=%s\n", r.ThreadID, r.TurnID, r.StartedAt)
	}
}

func truncateJSON(raw json.RawMessage, n int) string {
	s := string(raw)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

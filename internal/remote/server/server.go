package server

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"github.com/blueberrycongee/wuu/internal/remote/account"
	"github.com/blueberrycongee/wuu/internal/remote/relay"
	"github.com/blueberrycongee/wuu/internal/statepath"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Run serves the account API and encrypted relay until ctx is canceled.
// It also accepts the legacy relay flags used by the desktop CLI.
func Run(ctx context.Context, args []string, output io.Writer) error {
	return run(ctx, args, output, false)
}

// RunStandalone requires a PostgreSQL database for the standalone account service.
func RunStandalone(ctx context.Context, args []string, output io.Writer) error {
	return run(ctx, args, output, true)
}

func run(ctx context.Context, args []string, output io.Writer, requireAccounts bool) error {
	fs := flag.NewFlagSet("relay", flag.ContinueOnError)
	fs.SetOutput(output)
	accountDB := fs.String("database-url", "", "PostgreSQL URL (defaults to WUU_DATABASE_URL)")
	registration := fs.Bool("registration", false, "allow account registration")
	trustedProxyFlag := fs.String("trusted-proxies", "", "comma-separated proxy IP prefixes trusted for X-Forwarded-For")
	addr := fs.String("addr", "127.0.0.1:8787", "listen address")
	webRoot := fs.String("web-root", "", "serve the built Wuu Web directory alongside the relay")
	publicURL := fs.String("public-url", "", "browser-facing http(s) origin, including reverse proxy TLS termination")
	tlsCert := fs.String("tls-cert", "", "TLS certificate PEM file")
	tlsKey := fs.String("tls-key", "", "TLS private key PEM file")
	statePath := fs.String("state", "", "registry file (default <wuu home>/relay-state.json)")
	pushWebhook := fs.String("push-webhook", "", "POST content-free push events to this URL")
	pushConfig := fs.String("push-config", "", "JSON file configuring operator-owned APNs/FCM credentials")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if *accountDB == "" {
		*accountDB = os.Getenv("WUU_DATABASE_URL")
	}
	if (requireAccounts || *registration) && *accountDB == "" {
		return errors.New("account service requires WUU_DATABASE_URL or --database-url")
	}
	var trustedProxies []netip.Prefix
	if strings.TrimSpace(*trustedProxyFlag) != "" {
		for _, raw := range strings.Split(*trustedProxyFlag, ",") {
			prefix, err := netip.ParsePrefix(strings.TrimSpace(raw))
			if err != nil {
				return fmt.Errorf("trusted-proxies: %w", err)
			}
			trustedProxies = append(trustedProxies, prefix)
		}
	}
	tlsConfig, err := remoteRelayTLS(*tlsCert, *tlsKey)
	if err != nil {
		return err
	}
	if *publicURL != "" {
		if _, err := remoteRelayURL(*publicURL, "", tlsConfig != nil); err != nil {
			return err
		}
	}

	regPath := strings.TrimSpace(*statePath)
	if regPath == "" {
		home, err := statepath.Home(os.Getenv("HOME"))
		if err != nil {
			return err
		}
		regPath = filepath.Join(home, "relay-state.json")
	}
	reg, err := relay.OpenRegistry(regPath)
	if err != nil {
		return err
	}
	var pusher relay.Pusher
	if strings.TrimSpace(*pushWebhook) != "" {
		pusher = relay.WebhookPusher{URL: strings.TrimSpace(*pushWebhook)}
	}
	var accounts *account.Store
	if *accountDB != "" {
		accounts, err = account.Open(*accountDB)
		if err != nil {
			return err
		}
		defer accounts.Close()
	}
	var pushPlatforms []string
	if *pushConfig != "" {
		if *pushWebhook != "" {
			return errors.New("choose push-config or push-webhook")
		}
		native, err := relay.NewNativePusher(*pushConfig, accounts, func(format string, args ...any) { fmt.Fprintf(output, format+"\n", args...) })
		if err != nil {
			return err
		}
		pusher = native
		pushPlatforms = native.Platforms()
	}
	github, err := account.NewGitHubAuth(account.GitHubConfig{ClientID: os.Getenv("WUU_GITHUB_CLIENT_ID"), ClientSecret: os.Getenv("WUU_GITHUB_CLIENT_SECRET"), PublicURL: os.Getenv("WUU_ACCOUNT_PUBLIC_URL")})
	if err != nil {
		return err
	}
	if github != nil && accounts == nil {
		return errors.New("GitHub login requires an account database")
	}
	srv := relay.New(relay.Options{Registry: reg, Pusher: pusher, Accounts: accounts, GitHub: github, AllowRegistration: *registration, PushPlatforms: pushPlatforms, TrustedProxies: trustedProxies})

	defer srv.Close()
	handler := srv.Handler()
	if *webRoot != "" {
		if _, err := os.Stat(filepath.Join(*webRoot, "index.html")); err != nil {
			return fmt.Errorf("Wuu Web build: %w", err)
		}
		mux := http.NewServeMux()
		mux.Handle("/v1/", handler)
		mux.Handle("/healthz", handler)
		mux.Handle("/", remoteWebHandler(*webRoot))
		handler = mux
	}
	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	defer listener.Close()
	connectURL, err := remoteRelayURL(*publicURL, listener.Addr().String(), tlsConfig != nil)
	if err != nil {
		return err
	}
	if tlsConfig != nil {
		listener = tls.NewListener(listener, tlsConfig)
	}
	httpServer := &http.Server{Addr: *addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	shutdownCtx, stopShutdown := context.WithCancel(ctx)
	defer stopShutdown()
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-shutdownCtx.Done()
		drainCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(drainCtx); err != nil {
			_ = httpServer.Close()
		}
	}()

	fmt.Fprintf(output, "wuu relay listening on %s (registry: %s)\n", listener.Addr().String(), regPath)
	fmt.Fprintf(output, "connect url: %s\n", connectURL)
	err = httpServer.Serve(listener)
	stopShutdown()
	<-shutdownDone
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func remoteRelayTLS(certFile, keyFile string) (*tls.Config, error) {
	if certFile == "" && keyFile == "" {
		return nil, nil
	}
	if certFile == "" || keyFile == "" {
		return nil, errors.New("--tls-cert and --tls-key must be supplied together")
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}, nil
}

func remoteRelayURL(publicURL, address string, encrypted bool) (string, error) {
	if publicURL == "" {
		scheme := "ws"
		if encrypted {
			scheme = "wss"
		}
		return scheme + "://" + address + "/v1/connect", nil
	}
	u, err := url.Parse(publicURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", errors.New("--public-url must be an http(s) origin without credentials, path, query or fragment")
	}
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	u.Path = "/v1/connect"
	return u.String(), nil
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/providers/xaisub"
)

func runLogin(args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	noOpen := fs.Bool("no-open", false, "do not open the verification URL")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("usage: wuu login xai")
	}
	provider := strings.ToLower(strings.TrimSpace(fs.Arg(0)))
	if provider != "xai" && provider != "xai-subscription" && provider != "grok" && provider != "supergrok" {
		return fmt.Errorf("unsupported login provider %q (supported: xai)", fs.Arg(0))
	}

	ctx, cancel := signalContext()
	defer cancel()
	binding, err := openCommandState(ctx, "")
	if err != nil {
		return err
	}
	defer binding.Close()
	services, err := binding.Services()
	if err != nil {
		return err
	}
	device, err := xaisub.RequestDeviceCode(ctx, nil)
	if err != nil {
		return err
	}
	verifyURL := device.VerificationURIComplete
	if strings.TrimSpace(verifyURL) == "" {
		verifyURL = device.VerificationURI
	}
	fmt.Println("Sign in with SuperGrok or X Premium+")
	fmt.Printf("Open %s\n", verifyURL)
	if device.UserCode != "" {
		fmt.Printf("Enter code: %s\n", device.UserCode)
	}
	if !*noOpen {
		_ = openBrowser(verifyURL)
	}
	tokens, err := xaisub.WaitForDevice(ctx, device)
	if err != nil {
		return err
	}
	if err := xaisub.SaveTokens(ctx, services.Credentials, tokens, xaisub.DefaultBaseURL); err != nil {
		return err
	}
	if err := ensureXAISubscriptionProvider(ctx, services.Config); err != nil {
		return err
	}
	fmt.Println("Signed in. Use provider xai-subscription, or set it as default_provider.")
	return nil
}

func ensureXAISubscriptionProvider(ctx context.Context, selected *config.Client) error {
	view, err := selected.Read(ctx)
	if err != nil {
		return err
	}
	source := view.Source
	if len(source) == 0 {
		source, err = json.Marshal(view.Config)
		if err != nil {
			return err
		}
	}
	next, err := config.AddProviderIfMissingSource(source, xaisub.AuthProviderID, config.ProviderConfig{Type: "xai-subscription", BaseURL: xaisub.DefaultBaseURL, WireAPI: "responses", Model: xaisub.DefaultModel})
	if err != nil {
		return err
	}
	if bytes.Equal(source, next) {
		return nil
	}
	committed, err := selected.CompareExchange(ctx, view.Revision, next)
	if err != nil {
		return err
	}
	if committed.ActivationError != "" {
		return errors.New("signed in and saved the provider, but configuration activation failed")
	}
	return nil
}

func openBrowser(rawURL string) error {
	if strings.TrimSpace(rawURL) == "" {
		return errors.New("empty url")
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", rawURL)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL)
	default:
		cmd = exec.Command("xdg-open", rawURL)
	}
	return cmd.Start()
}

func signalContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 15*time.Minute)
}

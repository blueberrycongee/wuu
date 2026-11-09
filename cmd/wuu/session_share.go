package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
)

// runSessionShare is the supported local-owner workflow. Recipient applications
// use these same RPC methods on an authenticated transport, not owner CLI flags.
func runSessionShare(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: wuu session-share {preview|create|read|attachment|revoke} [--workdir DIR] JSON")
	}
	methods := map[string]string{"preview": "session/share/preview", "create": "session/share/create", "read": "session/share/read", "attachment": "session/share/attachment/read", "revoke": "session/share/revoke"}
	method, ok := methods[args[0]]
	if !ok {
		return errors.New("unknown sharing action; expected preview, create, read, attachment or revoke")
	}
	fs := flag.NewFlagSet("session-share "+args[0], flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cfg := addDebugAppServerFlags(fs)
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	params, err := parseDebugJSONParams(fs.Args())
	if err != nil {
		return err
	}
	client, err := newDebugAppServerClient(context.Background(), debugAppServerOptionsFromCLI(cfg))
	if err != nil {
		return err
	}
	defer shutdownDebugClient(client)
	var result json.RawMessage
	if err := client.Call(context.Background(), method, params, &result); err != nil {
		return err
	}
	return printRawJSON(result)
}

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/personalconnectors"
	"github.com/blueberrycongee/wuu/internal/userspace"
	"io"
	"os"
)

// Connector configuration is an explicit trusted-host operation. Neither tool
// input nor plugin settings can install credential material.
func runConnectorClient(args []string) error {
	if len(args) < 1 {
		return errors.New("usage: wuu connector-client import --user-space-id SPACE (JSON from stdin)")
	}
	operation := args[0]
	fs := flag.NewFlagSet("connector-client", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	space := fs.String("user-space-id", "", "Personal user space ID")
	if err := fs.Parse(args[1:]); err != nil {
		return errors.New("invalid connector-client arguments")
	}
	binding := userspace.Binding{UserSpaceID: *space, ProductDomain: "personal"}
	if fs.NArg() != 0 || binding.Validate() != nil || !binding.IsUser() || (operation != "import" && operation != "client-import") {
		return errors.New("usage: wuu connector-client import|client-import --user-space-id SPACE (JSON from stdin)")
	}
	store, err := credentialstore.NewDesktopStore()
	if err != nil {
		return errors.New("Connector secure store is unavailable")
	}
	if operation == "client-import" {
		provider, err := personalconnectors.ImportClient(context.Background(), store, os.Stdin)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"provider": provider, "state": "client_configured", "authorization": "not_started"})
	}
	broker := personalconnectors.New(personalconnectors.Options{Store: store})
	defer broker.Close()
	connection, err := broker.Import(context.Background(), personalconnectors.Actor{ProductDomain: "personal", UserSpaceID: *space, SessionID: "owner-cli"}, os.Stdin)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(connection)
}

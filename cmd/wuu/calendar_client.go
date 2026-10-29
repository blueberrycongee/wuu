package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/personalcalendar"
)

// This explicit host command imports an existing approved OAuth application;
// it never creates a Google client, requests tokens, or opens an account login.
func runCalendarClient(args []string) error {
	if len(args) != 1 || args[0] != "import" {
		return errors.New("usage: wuu calendar-client import (read client_id and client_secret JSON from stdin)")
	}
	store, err := credentialstore.NewDesktopStore()
	if err != nil {
		return errors.New("Calendar secure store is unavailable")
	}
	id, err := personalcalendar.ImportDesktopClient(context.Background(), store, os.Stdin)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "Imported Calendar client %s into host secure storage. Account authorization has not started.\n", id)
	return nil
}

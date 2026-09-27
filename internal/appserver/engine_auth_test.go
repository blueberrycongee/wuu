package appserver

import (
	"context"
	"errors"
	"testing"
)

func TestEngineAuthCancellationAndShutdownOwnAdmittedWork(t *testing.T) {
	s := &Server{}
	ctx, release, err := s.beginEngineAuth(context.Background(), "cursor")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.beginEngineAuth(context.Background(), "cursor"); err == nil {
		t.Fatal("duplicate sign-in admitted")
	}
	other, releaseOther, err := s.beginEngineAuth(context.Background(), "antigravity")
	if err != nil {
		t.Fatal(err)
	}
	defer releaseOther()
	s.cancelEngineAuth("cursor")
	if !errors.Is(ctx.Err(), context.Canceled) || other.Err() != nil {
		t.Fatal("cancellation did not target the selected engine")
	}
	release()
	ctx, release, err = s.beginEngineAuth(context.Background(), "cursor")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	s.Close()
	if !errors.Is(ctx.Err(), context.Canceled) || !errors.Is(other.Err(), context.Canceled) {
		t.Fatal("shutdown did not cancel all admitted sign-in operations")
	}
	if _, _, err := s.beginEngineAuth(context.Background(), "devin"); !errors.Is(err, errServerClosed) {
		t.Fatalf("sign-in admitted after shutdown: %v", err)
	}
}

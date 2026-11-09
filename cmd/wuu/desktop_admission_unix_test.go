//go:build !windows

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/clientauthority"
	"github.com/blueberrycongee/wuu/internal/userspace"
)

func TestAdmitDesktopParentAcceptsInheritedBlockingPipe(t *testing.T) {
	var descriptors [2]int
	if err := syscall.Pipe(descriptors[:]); err != nil {
		t.Fatalf("create blocking pipe: %v", err)
	}
	reader, err := openDesktopAdmissionPipe(uintptr(descriptors[0]))
	if err != nil {
		t.Fatal(err)
	}
	writer := os.NewFile(uintptr(descriptors[1]), "desktop-parent-admission-writer")
	t.Cleanup(func() { _ = reader.Close() })
	t.Cleanup(func() { _ = writer.Close() })

	binding := userspace.Binding{UserSpaceID: "local", ProductDomain: "personal"}
	writeErr := make(chan error, 1)
	go func() {
		_, err := writer.WriteString(`{"version":1,"user_space_id":"local","product_domain":"personal"}`)
		if closeErr := writer.Close(); err == nil {
			err = closeErr
		}
		writeErr <- err
	}()

	admitted, err := admitDesktopParent(context.Background(), binding, reader)
	if err != nil {
		t.Fatalf("admit desktop parent: %v", err)
	}
	if err := <-writeErr; err != nil {
		t.Fatalf("write desktop admission: %v", err)
	}
	if err := clientauthority.RequireOwner(admitted, binding); err != nil {
		t.Fatalf("desktop owner authority: %v", err)
	}
}

func TestAdmitDesktopParentStopsWithOpenParent(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancelled=%t", cancelled), func(t *testing.T) {
			var descriptors [2]int
			if err := syscall.Pipe(descriptors[:]); err != nil {
				t.Fatal(err)
			}
			reader, err := openDesktopAdmissionPipe(uintptr(descriptors[0]))
			if err != nil {
				t.Fatal(err)
			}
			writer := os.NewFile(uintptr(descriptors[1]), "parent")
			defer reader.Close()
			defer writer.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			completed := make(chan error, 1)
			go func() {
				_, err := admitDesktopParent(ctx, userspace.Binding{UserSpaceID: "local", ProductDomain: "personal"}, reader)
				completed <- err
			}()
			limit := 4 * time.Second
			if cancelled {
				cancel()
				limit = time.Second
			}
			select {
			case err := <-completed:
				if cancelled && !errors.Is(err, context.Canceled) {
					t.Fatalf("expected cancellation, got %v", err)
				}
				if !cancelled && (err == nil || err.Error() != "desktop owner admission timed out") {
					t.Fatalf("expected admission timeout, got %v", err)
				}
			case <-time.After(limit):
				t.Fatal("admission remained blocked with its parent channel open")
			}
		})
	}
}

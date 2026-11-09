package main

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/blueberrycongee/wuu/internal/clientauthority"
	"github.com/blueberrycongee/wuu/internal/userspace"
)

func admitDesktopParent(ctx context.Context, binding userspace.Binding, bootstrap *os.File) (context.Context, error) {
	type result struct {
		ctx context.Context
		err error
	}
	completed := make(chan result, 1)
	go func() {
		admitted, err := clientauthority.AdmitDesktop(ctx, binding, bootstrap)
		completed <- result{ctx: admitted, err: err}
	}()

	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	select {
	case admitted := <-completed:
		return admitted.ctx, admitted.err
	case <-ctx.Done():
		_ = bootstrap.Close()
		<-completed
		return nil, ctx.Err()
	case <-timer.C:
		_ = bootstrap.Close()
		<-completed
		return nil, errors.New("desktop owner admission timed out")
	}
}

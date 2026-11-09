//go:build windows

package executionworker

import (
	"context"
	"errors"
	"io"
)

func Connect(context.Context, string, int, io.Reader, io.Writer) error {
	return errors.New("execution workers require a Unix environment")
}
func ServeSocket(context.Context, string, int) error {
	return errors.New("execution workers require a Unix environment")
}

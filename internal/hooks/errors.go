package hooks

import "errors"

// ErrBlocked is returned when a hook blocks the operation (exit code 2 or decision "block").
var ErrBlocked = errors.New("operation blocked by hook")

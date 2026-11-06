package toolerrors

import "errors"

// UncertainEffect means execution may have changed external state but no
// authoritative result was received. Callers must not retry the effect.
const UncertainEffect = "uncertain_tool_effect"

type uncertainEffectError struct{ cause error }

func (e *uncertainEffectError) Error() string         { return e.cause.Error() }
func (e *uncertainEffectError) Unwrap() error         { return e.cause }
func (e *uncertainEffectError) ToolErrorKind() string { return UncertainEffect }

// Unknown preserves the cause, including cancellation, without claiming failure
// or successful completion of an operation that may already have executed.
func Unknown(cause error) error {
	if cause == nil {
		return nil
	}
	if Kind(cause) == UncertainEffect {
		return cause
	}
	return &uncertainEffectError{cause: cause}
}

const InvalidArguments = "invalid_tool_arguments"

type Error struct {
	kind    string
	message string
}

func New(kind, message string) error {
	return &Error{kind: kind, message: message}
}

func (e *Error) Error() string {
	return e.message
}

func (e *Error) ToolErrorKind() string {
	return e.kind
}

func Kind(err error) string {
	var typed interface{ ToolErrorKind() string }
	if errors.As(err, &typed) {
		return typed.ToolErrorKind()
	}
	return ""
}

package toolerrors

import "errors"

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

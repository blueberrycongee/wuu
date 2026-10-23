package session

import (
	"errors"

	"strings"
)

const (
	HeldUserWorkOriginQueue = "queue"
	HeldUserWorkOriginSteer = "steer"
)

type HeldUserWork struct {
	SessionID           string
	Position            int
	ID                  string
	Origin              string
	MessageJSON         []byte
	RuntimeJSON         []byte
	DeliveryUnconfirmed bool
}

// ErrHeldUserWorkSettled prevents an old client submission from becoming new
// work after cancellation or admission, including after history is compacted.
var ErrHeldUserWorkSettled = errors.New("accepted user input was already consumed or cancelled")

// ErrHeldUserWorkUnconfirmed prevents replay while native delivery is in flight
// or its acknowledgement was lost. The payload remains visible for recovery.
var ErrHeldUserWorkUnconfirmed = errors.New("native input delivery is unconfirmed; inspect the native conversation before sending any replacement")

func validateHeldUserWork(entry HeldUserWork) error {
	if strings.TrimSpace(entry.ID) == "" || len(entry.MessageJSON) == 0 || len(entry.RuntimeJSON) == 0 {
		return errors.New("held user work requires id, message and runtime")
	}
	if entry.Origin != HeldUserWorkOriginQueue && entry.Origin != HeldUserWorkOriginSteer {
		return errors.New("invalid held user work origin")
	}
	return nil
}

package toolledger

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"strings"
)

type BatchState string

const (
	BatchCollecting  BatchState = "collecting"
	BatchFinalized   BatchState = "finalized"
	BatchSettled     BatchState = "settled"
	BatchProjected   BatchState = "projected"
	BatchAbandoned   BatchState = "abandoned"
	BatchInterrupted BatchState = "interrupted"
)

type InvocationState string

const (
	InvocationPrepared           InvocationState = "prepared"
	InvocationRunning            InvocationState = "running"
	InvocationSucceeded          InvocationState = "succeeded"
	InvocationFailed             InvocationState = "failed"
	InvocationInterruptedUnknown InvocationState = "interrupted_unknown"
	InvocationAbandoned          InvocationState = "abandoned"
)

type ReplayPolicy string

const (
	ReplayAtMostOnce ReplayPolicy = "at_most_once"
	ReplayRepeatable ReplayPolicy = "repeatable"
	ReplayIdempotent ReplayPolicy = "idempotent"
)

type ReplayAction string

const (
	ReplayAllow ReplayAction = "allow"
	ReplayBlock ReplayAction = "block"
)

type ReplayReason string

const (
	ReplayReasonNoInvocation      ReplayReason = "no_invocation"
	ReplayReasonPreparedOnly      ReplayReason = "prepared_only"
	ReplayReasonInvocationRunning ReplayReason = "invocation_running"
	ReplayReasonInvocationSettled ReplayReason = "invocation_settled"
	ReplayReasonInvocationUnknown ReplayReason = "invocation_unknown"
)

type ReplayDecision struct {
	Action                ReplayAction
	Reason                ReplayReason
	BlockingInvocationIDs []string
	SupersedePartial      bool
}

type ReplayBlockedError struct {
	Decision ReplayDecision
}

func (e *ReplayBlockedError) Error() string {
	if e == nil {
		return "tool replay blocked"
	}
	return fmt.Sprintf("tool replay blocked: %s (%s)", e.Decision.Reason, strings.Join(e.Decision.BlockingInvocationIDs, ","))
}

func (e *ReplayBlockedError) ReplayReasonCode() string {
	if e == nil {
		return ""
	}
	return string(e.Decision.Reason)
}

type Invocation struct {
	ParentInvocationID string
	ID                 string
	BatchID            string
	ProviderCallID     string
	ToolName           string
	ToolKind           providers.ToolCallKind
	Arguments          string
	ReplayPolicy       ReplayPolicy
	State              InvocationState
	Result             toolresult.Result
	Projected          bool
}

func newID(prefix string) string {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic(fmt.Sprintf("toolledger: generate id: %v", err))
	}
	return prefix + "-" + hex.EncodeToString(raw[:])
}

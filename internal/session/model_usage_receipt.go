package session

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
)

var modelUsageDomainMethods = []string{"ReadModelUsageReceipts"}

// ReadModelUsageReceipts reads authoritative submission metadata only. The
// selected provider must implement this operation; no local fallback is used.
func (s *Store) ReadModelUsageReceipts(sessionID, driverID, requestID string) (pluginprotocol.DriverModelReceipts, error) {
	var result pluginprotocol.DriverModelReceipts
	err := s.invoke("ReadModelUsageReceipts", []any{sessionID, driverID, requestID}, []any{&result})
	return result, err
}

func (s *documentStore) ReadModelUsageReceipts(sessionID, driverID, requestID string) (pluginprotocol.DriverModelReceipts, error) {
	result := pluginprotocol.DriverModelReceipts{Status: "unavailable", Receipts: []pluginprotocol.DriverModelUsageReceipt{}}
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(driverID) == "" || strings.TrimSpace(requestID) == "" || len(requestID) > 256 {
		return result, errors.New("session, driver and bounded model request id are required")
	}
	err := s.transaction(func(tx *persistence.Tx) error {
		rows, err := readModelReceiptDocuments(tx, sessionID)
		if err != nil {
			return err
		}
		result = pluginprotocol.DriverModelReceipts{Status: "unavailable", Receipts: []pluginprotocol.DriverModelUsageReceipt{}}
		seen := map[string]bool{}
		for _, row := range rows {
			if row.DriverID != driverID {
				continue
			}
			var input struct {
				RequestID string `json:"request_id"`
				Model     string `json:"model"`
			}
			if err := json.Unmarshal(row.Payload, &input); err != nil {
				return err
			}
			if input.RequestID != requestID {
				continue
			}
			var op inferenceOperationDocument
			found, err := tx.Get(documentNamespace("inference_operations"), row.OperationID, &op)
			if err != nil {
				return err
			}
			if !found {
				continue
			}
			if op.OwnerID != sessionID {
				return errors.New("model operation owner does not match session")
			}
			if result.Status == "unavailable" {
				result.Status = "complete"
			}
			if op.Status == "active" {
				result.Status = "incomplete"
			}
			for _, attemptID := range op.AttemptIDs {
				attempt, err := requireInferenceDocument[inferenceAttemptDocument](tx, "inference_attempts", attemptID)
				if err != nil {
					return err
				}
				if attempt.OperationID != op.ID {
					return errors.New("model attempt operation mismatch")
				}
				for _, id := range attempt.SubmissionIDs {
					sub, err := requireInferenceDocument[providers.InferenceSubmissionJournalRecord](tx, "inference_submissions", id)
					if err != nil {
						return err
					}
					if sub.OperationID != op.ID || sub.AttemptID != attemptID {
						return errors.New("model submission operation mismatch")
					}
					if seen[id] {
						continue
					}
					seen[id] = true
					receipt := pluginprotocol.DriverModelUsageReceipt{OperationID: op.ID, SubmissionID: id, Provider: sub.Provider, Model: input.Model, Outcome: string(sub.Outcome), CostState: string(sub.CostState)}
					if sub.ReportedUsage != nil {
						if sub.CostState != providers.InferenceCostKnown || sub.Outcome == providers.InferenceSubmissionInFlight || sub.ReportedUsage.CacheCreationUnknown {
							result.Status = "incomplete"
						}
						receipt.Usage = &pluginprotocol.ModelRequestUsage{InputTokens: sub.ReportedUsage.InputTokens, OutputTokens: sub.ReportedUsage.OutputTokens, CacheCreationTokens: sub.ReportedUsage.CacheCreationTokens, CacheReadTokens: sub.ReportedUsage.CacheReadTokens, CacheCreationUnknown: sub.ReportedUsage.CacheCreationUnknown}
					} else {
						result.Status = "incomplete"
					}
					result.Receipts = append(result.Receipts, receipt)
				}
			}
			if len(result.Receipts) == 0 {
				result.Status = "incomplete"
			}
		}
		return nil
	})
	return result, err
}

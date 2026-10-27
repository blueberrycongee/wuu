package session

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/participant"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/session/legacysqlite"
	"github.com/blueberrycongee/wuu/internal/storagecodec"
)

// LegacyTableConverter converts an entire owned legacy table to documents.
// Values use SQLite types: nil, int64, float64, string, or []byte. Converters
// return claimed=false for tables outside their domain and perform no effects.
// Accepted values are snapshotted so later calls cannot alter an earlier write.
// After every table, converters receive table="" and nil rows once to finalize
// cross-table indexes. Supply fresh converter instances for each migration call.
// The composition supplies converters explicitly; no global registry is used.
type LegacyTableConverter func(table string, rows []map[string]any) ([]persistence.Write, bool, error)

type LegacyMigrationResult struct {
	Fingerprint     string         `json:"fingerprint"`
	Rows            map[string]int `json:"rows"`
	AlreadyMigrated bool           `json:"-"`
}

// LegacySQLiteMigrationRecorded reports whether the selected authority has a
// completed legacy conversion. This bounded marker read does not verify that
// the source stayed unchanged; callers must still run MigrateLegacySQLite.
func LegacySQLiteMigrationRecorded(ctx context.Context, documents persistence.Documents) (bool, error) {
	_, recorded, err := readLegacySQLiteMigration(ctx, documents)
	return recorded, err
}

func readLegacySQLiteMigration(ctx context.Context, documents persistence.Documents) (LegacyMigrationResult, bool, error) {
	var result LegacyMigrationResult
	view, err := documents.Read(ctx, persistence.Query{Namespace: documentNamespace("legacy_sqlite_migrations"), Keys: []string{dbFileName}})
	if err != nil || len(view.Documents) == 0 {
		return result, false, err
	}
	if err := json.Unmarshal(view.Documents[0].Value, &result); err != nil {
		return result, false, fmt.Errorf("%w: invalid legacy migration marker", persistence.ErrCorrupt)
	}
	if result.Fingerprint == "" {
		return result, false, fmt.Errorf("%w: incomplete legacy migration marker", persistence.ErrCorrupt)
	}
	return result, true, nil
}

// MigrateLegacySQLite imports a stopped legacy writer into the selected provider.
// Every converted document, raw source row and completion marker commits in one
// transaction. Conflicting destination namespaces are rejected. An uncertain
// commit returns persistence.CommitError for retrying the exact transaction;
// Repeating migration after success streams the source fingerprint without
// retaining rows, running converters or constructing another write set.
// The source is never changed, and unknown nonempty tables fail closed.
func MigrateLegacySQLite(ctx context.Context, sessDir string, documents persistence.Documents, converters ...LegacyTableConverter) (LegacyMigrationResult, error) {
	previous, recorded, err := readLegacySQLiteMigration(ctx, documents)
	if err != nil {
		return LegacyMigrationResult{}, err
	}
	if recorded {
		fingerprint, exists, err := legacysqlite.Fingerprint(ctx, filepath.Join(sessDir, dbFileName))
		if err != nil || !exists {
			return LegacyMigrationResult{}, err
		}
		if previous.Fingerprint != fingerprint {
			return LegacyMigrationResult{}, fmt.Errorf("%w: legacy source changed after migration", persistence.ErrMigrationRequired)
		}
		previous.AlreadyMigrated = true
		return previous, nil
	}
	snapshot, exists, err := legacysqlite.Read(ctx, filepath.Join(sessDir, dbFileName))
	if err != nil || !exists {
		return LegacyMigrationResult{}, err
	}
	result := LegacyMigrationResult{Fingerprint: snapshot.Fingerprint, Rows: make(map[string]int)}
	writes := make(map[string]persistence.PreparedWrite)
	namespaces := make(map[string]bool)
	add := func(write persistence.Write) error {
		key := write.Namespace + "\x00" + write.Key
		if write.Delete || write.Namespace == "" || write.Key == "" || !json.Valid(write.Value) {
			return fmt.Errorf("%w: invalid legacy conversion write", persistence.ErrInvalid)
		}
		if _, found := writes[key]; found {
			return fmt.Errorf("%w: duplicate legacy conversion for %s/%s", persistence.ErrConflict, write.Namespace, write.Key)
		}
		prepared, err := persistence.PrepareWrite(write.Namespace, write.Key, write.Value)
		if err != nil {
			return err
		}
		writes[key] = prepared
		namespaces[write.Namespace] = true
		return nil
	}
	put := func(table, key string, value any) error {
		raw, err := json.Marshal(value)
		if err != nil {
			return err
		}
		return add(persistence.Write{Namespace: documentNamespace(table), Key: key, Value: raw})
	}
	conversion := legacySessionConversion{put: put, sessions: make(map[string]Session), heads: make(map[string]int), retractions: make(map[string][]historyRetraction), checkpoints: make(map[string]HistoryCheckpoint)}
	for _, table := range snapshot.Tables {
		result.Rows[table.Name] = len(table.Rows)
		rows := make([]map[string]any, len(table.Rows))
		for i, row := range table.Rows {
			rows[i] = row.Values
		}
		claimed, err := conversion.table(table.Name, rows)
		if err != nil {
			return result, fmt.Errorf("convert legacy %s: %w", table.Name, err)
		}
		if !claimed {
			for _, converter := range converters {
				converted, owns, err := converter(table.Name, rows)
				if err != nil {
					return result, fmt.Errorf("convert legacy %s: %w", table.Name, err)
				}
				if !owns {
					continue
				}
				if claimed {
					return result, fmt.Errorf("multiple converters own legacy table %s", table.Name)
				}
				claimed = true
				for _, write := range converted {
					if err := add(write); err != nil {
						return result, err
					}
				}
			}
		}
		if !claimed && len(rows) > 0 {
			return result, fmt.Errorf("%w: no converter for nonempty legacy table %s", persistence.ErrMigrationRequired, table.Name)
		}
		if err := put("legacy_sqlite_schema", table.Name, table.Schema); err != nil {
			return result, err
		}
		for i, row := range table.Rows {
			if err := put("legacy_sqlite_rows", documentPrefix(table.Name)+fmt.Sprintf("%020d", i), row); err != nil {
				return result, err
			}
		}
	}
	for _, converter := range converters {
		converted, _, err := converter("", nil)
		if err != nil {
			return result, fmt.Errorf("finalize legacy conversion: %w", err)
		}
		for _, write := range converted {
			if err := add(write); err != nil {
				return result, err
			}
		}
	}

	if err := conversion.finish(); err != nil {
		return result, err
	}
	err = persistence.Update(ctx, documents, func(tx *persistence.Tx) error {
		var previous LegacyMigrationResult
		found, err := tx.Get(documentNamespace("legacy_sqlite_migrations"), dbFileName, &previous)
		if err != nil {
			return err
		}
		if found {
			if previous.Fingerprint != result.Fingerprint {
				return fmt.Errorf("%w: legacy source changed after migration", persistence.ErrMigrationRequired)
			}
			result.AlreadyMigrated = true
			return nil
		}
		result.AlreadyMigrated = false
		for namespace := range namespaces {
			existing, err := tx.Read(persistence.Query{Namespace: namespace, Limit: 1})
			if err != nil {
				return err
			}
			if len(existing) > 0 {
				return fmt.Errorf("%w: legacy destination namespace %s is not empty", persistence.ErrMigrationRequired, namespace)
			}
		}
		for _, write := range writes {
			if err := tx.PutPrepared(write); err != nil {
				return err
			}
		}
		return tx.Put(documentNamespace("legacy_sqlite_migrations"), dbFileName, result)
	})
	return result, err
}

type legacySessionConversion struct {
	put               func(table, key string, value any) error
	sessions          map[string]Session
	heads             map[string]int
	retractions       map[string][]historyRetraction
	checkpoints       map[string]HistoryCheckpoint
	terminal          map[string]HistoryRecord
	bindingsPresent   bool
	driverCheckpoints map[string]DriverCheckpointRecord
}

func (c *legacySessionConversion) table(table string, rows []map[string]any) (bool, error) {
	switch table {
	case "sessions", "session_messages", "session_folders", "session_controls", "project_work", "project_work_operations", "session_inbox", "plugin_turn_lifecycle_outbox", "plugin_turn_lifecycle", "held_user_work", "session_history_retractions", "session_history_checkpoints", "session_driver_bindings", "session_driver_checkpoints", "session_compaction_notes", "session_model_input_receipts", "participants", "participant_runs", "thread_execution_resets", "session_plugin_generation_snapshots", "context_seeds", "session_launches", "client_input_receipts":
	default:
		return false, nil
	}
	if table == "session_driver_bindings" {
		c.bindingsPresent = true
	}
	for _, row := range rows {
		id, _ := row["id"].(string)
		sessionID, _ := row["session_id"].(string)
		var value any
		key := id
		switch table {
		case "sessions":
			var sess Session
			if err := decodeLegacyRow(row, &sess, nil); err != nil {
				return true, err
			}
			c.sessions[sess.ID] = sess
			if raw := legacyBytes(row["request_context_json"]); len(raw) > 0 {
				if !json.Valid(raw) {
					return true, fmt.Errorf("invalid request context JSON")
				}
				if err := c.put("session_request_context", sess.ID, json.RawMessage(raw)); err != nil {
					return true, err
				}
			}
			continue
		case "session_messages":
			var record HistoryRecord
			if err := decodeLegacyRow(row, &record, nil); err != nil {
				return true, err
			}
			key = historyDocumentKey(sessionID, record.Seq)
			value = record
			c.heads[sessionID] = max(c.heads[sessionID], record.Seq)
			if record.Role == "user" || record.Role == "meta" && (record.Content == tokenUsageContent || record.Content == turnTerminalContent) {
				activity := HistoryRecord{ModelSteps: record.ModelSteps, Seq: record.Seq, Role: record.Role, Steered: record.Steered, ClientID: record.ClientID, StopReason: record.StopReason, DisplayContent: record.DisplayContent, At: record.At, Provider: record.Provider, Model: record.Model, InputTokens: record.InputTokens, OutputTokens: record.OutputTokens, CacheCreationTokens: record.CacheCreationTokens, CacheReadTokens: record.CacheReadTokens}
				if record.Role == "meta" {
					activity.Content = record.Content
				}
				if err := c.put("session_activity", key, activityDocument{SessionID: sessionID, Record: activity}); err != nil {
					return true, err
				}
			}
			if record.Role == "meta" && record.Content == turnTerminalContent && record.ClientID != "" {
				if c.terminal == nil {
					c.terminal = make(map[string]HistoryRecord)
				}
				if record.Seq > c.terminal[sessionID].Seq {
					c.terminal[sessionID] = record
				}
			}
		case "client_input_receipts":
			var receipt clientInputDocument
			if err := json.Unmarshal(legacyBytes(row["summary_json"]), &receipt.Receipt); err != nil {
				return true, fmt.Errorf("invalid accepted client input summary: %w", err)
			}
			// Keep durable host identities exact: a mismatched legacy row cannot
			// become another session's admissible envelope during conversion.
			if receipt.Receipt.SessionID != sessionID || receipt.Receipt.ClientID != legacyText(row["client_id"]) || receipt.Receipt.PluginID != legacyText(row["plugin_id"]) || receipt.Receipt.HostRequestID != legacyText(row["host_request_id"]) || receipt.Receipt.RequestID != legacyText(row["request_id"]) || receipt.Receipt.QueueID != legacyText(row["queue_id"]) {
				return true, fmt.Errorf("accepted client input identity does not match legacy row")
			}
			receipt.Fingerprint = legacyText(row["fingerprint"])
			receipt.LiveOwner = legacyText(row["live_owner"])
			receipt.Order, _ = row["__wuu_legacy_rowid"].(int64)
			key = clientInputDocumentKey(sessionID, receipt.Receipt.ClientID)
			payload := clientInputPayload{MessageJSON: legacyBytes(row["message_json"]), RuntimeJSON: legacyBytes(row["runtime_json"]), SelectionJSON: legacyBytes(row["selection_json"])}
			if err := c.put("client_input_payloads", key, payload); err != nil {
				return true, err
			}
			value = receipt
		case "session_folders":
			value = &OrganizationGroup{}
		case "session_controls":
			key = sessionID
			value = &Control{}
		case "participants":
			value = &participant.Participant{}
		case "participant_runs":
			value = &ParticipantRun{}
		case "session_driver_bindings":
			key = sessionID
			value = &pluginprotocol.DriverBinding{}
		case "session_driver_checkpoints":
			key = sessionID
			var checkpoint DriverCheckpointRecord
			if err := decodeLegacyRow(row, &checkpoint, nil); err != nil {
				return true, err
			}
			if c.driverCheckpoints == nil {
				c.driverCheckpoints = make(map[string]DriverCheckpointRecord)
			}
			c.driverCheckpoints[sessionID] = checkpoint
			value = checkpoint
		case "session_compaction_notes":
			key = documentPrefix(sessionID) + legacyText(row["provider_key"])
			value = &CompactionNote{}
		case "thread_execution_resets":
			key = legacyText(row["thread_id"])
			value = &threadExecutionDocument{}
		case "plugin_turn_lifecycle_outbox":
			key = documentPrefix(legacyText(row["plugin_id"])) + legacyText(row["request_id"])
			value = &PluginTurnLifecycleOutboxEntry{}
		case "plugin_turn_lifecycle":
			key = documentPrefix(legacyText(row["plugin_id"])) + legacyText(row["request_id"])
			value = &PluginTurnLifecycleEntry{}
		case "session_model_input_receipts":
			var receipt modelReceiptDocument
			if err := decodeLegacyRow(row, &receipt, map[string]string{"Payload": "receipt_json"}); err != nil {
				return true, err
			}
			decoded, err := storagecodec.Decode(receipt.Payload)
			if err != nil {
				return true, err
			}
			if !json.Valid(decoded) {
				return true, fmt.Errorf("invalid receipt JSON")
			}
			receipt.Payload = decoded
			key = documentPrefix(sessionID) + receipt.OperationID
			value = receipt
		case "held_user_work":
			var held heldWorkDocument
			if err := decodeLegacyRow(row, &held, nil); err != nil {
				return true, err
			}
			if held.State == "" {
				held.State = "pending"
			}
			key = heldWorkKey(sessionID, id)
			value = held
		case "session_inbox":
			var inbox inboxDocument
			if err := decodeLegacyRow(row, &inbox, map[string]string{"Order": "__wuu_legacy_rowid"}); err != nil {
				return true, err
			}
			if _, ok := row["wake"]; !ok {
				inbox.Wake = true
			}
			key = inbox.ClientID
			value = inbox
		case "project_work", "project_work_operations":
			var work ProjectWork
			if err := json.Unmarshal(legacyBytes(row["payload"]), &work); err != nil {
				return true, err
			}
			value = work
			if table == "project_work" {
				var stored projectWorkDocument
				stored.ProjectWork = work
				if n, ok := row["reconciled_version"].(int64); ok {
					stored.ReconciledVersion = int(n)
				}
				value = stored
			}
		case "session_history_retractions":
			var retraction historyRetraction
			if err := decodeLegacyRow(row, &retraction, nil); err != nil {
				return true, err
			}
			c.retractions[sessionID] = append(c.retractions[sessionID], retraction)
			continue
		case "session_history_checkpoints":
			var checkpoint HistoryCheckpoint
			if err := decodeLegacyRow(row, &checkpoint, nil); err != nil {
				return true, err
			}
			if checkpoint.Version > c.checkpoints[sessionID].Version {
				c.checkpoints[sessionID] = checkpoint
			}
			continue
		case "session_plugin_generation_snapshots":
			key = sessionID
			var snapshot PluginGenerationSnapshot
			if err := json.Unmarshal(legacyBytes(row["snapshot_json"]), &snapshot); err != nil {
				return true, err
			}
			value = snapshot
		case "context_seeds":
			var seed ContextSeed
			if err := decodeLegacyRow(row, &seed, nil); err != nil {
				return true, err
			}
			seed.Source.SessionID = legacyText(row["source_session_id"])
			if n, ok := row["source_through_seq"].(int64); ok {
				seed.Source.ThroughSeq = int(n)
			}
			value = seed
		case "session_launches":
			var launch SessionLaunchRecord
			if err := decodeLegacyRow(row, &launch, map[string]string{"SourceSession": "source_session_id", "SourceCutoff": "source_cutoff_seq", "TargetSession": "target_session_id"}); err != nil {
				return true, err
			}
			key = launch.RequestID
			value = launch
		}
		if reflect.TypeOf(value).Kind() == reflect.Pointer {
			if err := decodeLegacyRow(row, value, nil); err != nil {
				return true, err
			}
		}
		if err := c.put(table, key, value); err != nil {
			return true, err
		}
	}
	if table == "session_inbox" || table == "client_input_receipts" {
		var maxOrder int64
		for _, row := range rows {
			if n, ok := row["__wuu_legacy_rowid"].(int64); ok {
				maxOrder = max(maxOrder, n)
			}
		}
		sequence := "inbox"
		if table == "client_input_receipts" {
			sequence = "client_inputs"
		}
		if err := c.put("sequences", sequence, maxOrder); err != nil {
			return true, err
		}
	}
	return true, nil
}

func (c *legacySessionConversion) finish() error {
	for id, sess := range c.sessions {
		if terminal, ok := c.terminal[id]; ok {
			sess.LatestCompletedTurnID = terminal.ClientID
		}
		if err := c.put("sessions", id, sess); err != nil {
			return err
		}
		if err := c.put("session_order", sessionOrderKey(sess), id); err != nil {
			return err
		}
		if err := c.put("session_history_heads", id, c.heads[id]); err != nil {
			return err
		}
		// These identities describe the historical built-in driver, not a fallback
		// to the currently selected implementation. Unknown external identities stay unknown.
		if !c.bindingsPresent && (sess.EngineID == "" || sess.EngineID == "wuu") {
			binding := pluginprotocol.DriverBinding{Profile: "default", ProviderID: "wuu.default-loop", ContractVersion: 1, DriverID: "wuu.default-loop", DriverVersion: "1.0.0"}
			if checkpoint, found := c.driverCheckpoints[id]; found {
				binding.ContractVersion, binding.DriverID, binding.DriverVersion = checkpoint.ContractVersion, checkpoint.DriverID, checkpoint.DriverVersion
				if checkpoint.DriverID != "wuu.default-loop" {
					binding.Profile, binding.ProviderID = "", ""
				}
			}
			if err := c.put("session_driver_bindings", id, binding); err != nil {
				return err
			}
		}
	}
	if err := c.put("session_indexes", "listing", sessionListingIndexVersion); err != nil {
		return err
	}
	for id, retractions := range c.retractions {
		if err := c.put("session_history_retractions", id, retractions); err != nil {
			return err
		}
	}
	for id, checkpoint := range c.checkpoints {
		if err := c.put("session_history_checkpoints", id, checkpoint); err != nil {
			return err
		}
	}
	return nil
}

func legacyBytes(value any) []byte {
	switch value := value.(type) {
	case string:
		return []byte(value)
	case []byte:
		return value
	default:
		return nil
	}
}
func legacyText(value any) string { return string(legacyBytes(value)) }

// Reflection maps the versioned SQL column names to their typed domain fields;
// SQL-only and future columns remain intact in the raw archive. Typed decoding
// fails on malformed JSON/timestamps instead of silently manufacturing defaults.
func decodeLegacyRow(row map[string]any, target any, aliases map[string]string) error {
	normalize := func(name string) string { return strings.ToLower(strings.ReplaceAll(name, "_", "")) }
	columns := make(map[string]string, len(row))
	for column := range row {
		columns[normalize(column)] = column
	}
	var visit func(reflect.Value) error
	visit = func(value reflect.Value) error {
		for i := 0; i < value.NumField(); i++ {
			field := value.Field(i)
			spec := value.Type().Field(i)
			if spec.Anonymous {
				if err := visit(field); err != nil {
					return err
				}
				continue
			}
			if !field.CanSet() || spec.Tag.Get("json") == "-" {
				continue
			}
			column, ok := columns[normalize(spec.Name)]
			if !ok {
				column, ok = columns[normalize(spec.Name)+"json"]
			}
			if alias, exists := aliases[spec.Name]; exists {
				column = alias
				_, ok = row[column]
			}
			if !ok || row[column] == nil {
				continue
			}
			raw := row[column]
			dest := field
			if dest.Kind() == reflect.Pointer {
				// Empty legacy optional JSON/time columns represent absence,
				// not a populated zero-value pointer in the converted record.
				payload := strings.TrimSpace(legacyText(raw))
				if payload == "" || payload == "null" {
					continue
				}
				dest.Set(reflect.New(dest.Type().Elem()))
				dest = dest.Elem()
			}
			if dest.Type() == reflect.TypeFor[time.Time]() {
				text := legacyText(raw)
				if text == "" {
					continue
				}
				stamp, err := time.Parse(time.RFC3339Nano, text)
				if err != nil {
					return fmt.Errorf("invalid legacy timestamp in %s", column)
				}
				dest.Set(reflect.ValueOf(stamp))
				continue
			}
			switch dest.Kind() {
			case reflect.String:
				dest.SetString(legacyText(raw))
			case reflect.Bool:
				n, ok := raw.(int64)
				if !ok {
					return fmt.Errorf("invalid legacy boolean in %s", column)
				}
				dest.SetBool(n != 0)
			case reflect.Int, reflect.Int64:
				n, ok := raw.(int64)
				if !ok {
					return fmt.Errorf("invalid legacy integer in %s", column)
				}
				dest.SetInt(n)
			case reflect.Slice:
				payload := legacyBytes(raw)
				if dest.Type() == reflect.TypeFor[[]byte]() {
					dest.SetBytes(payload)
					continue
				}
				if len(payload) == 0 {
					continue
				}
				decoded, err := storagecodec.Decode(payload)
				if err != nil {
					return fmt.Errorf("decode legacy %s: %w", column, err)
				}
				if err := json.Unmarshal(decoded, dest.Addr().Interface()); err != nil {
					return fmt.Errorf("invalid legacy JSON in %s", column)
				}
			case reflect.Struct, reflect.Map:
				payload := legacyBytes(raw)
				if len(payload) == 0 {
					continue
				}
				if err := json.Unmarshal(payload, dest.Addr().Interface()); err != nil {
					return fmt.Errorf("invalid legacy JSON in %s", column)
				}
			default:
				return fmt.Errorf("unsupported legacy field %s", spec.Name)
			}
		}
		return nil
	}
	return visit(reflect.ValueOf(target).Elem())
}

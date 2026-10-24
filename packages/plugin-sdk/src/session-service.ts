import type { RuntimeHost, ServiceDescriptor, ServiceInvokeParams, ServiceRequirement } from "./index.js";

/** Complete session-domain operations. Storage and atomic domain transitions belong to the selected provider. */
export const SESSION_DOMAIN_SERVICE = "session.domain";
export const SESSION_DOMAIN_DEFAULT_MODULE = "wuu.session.default";
export interface SessionDomainJournalHandle { readonly handle: string; readonly runtime_id: string; }

export interface SessionDomainBudgetLimit {
  readonly "Set": boolean;
  readonly "Value": number;
}

export type SessionDomainClassificationConfidence = string;

export interface SessionDomainCompactionNote {
  readonly "CoveredHash": string;
  readonly "CoveredMessages": number;
  readonly "Markdown": string;
  readonly "ProviderKey": string;
  readonly "SessionID": string;
  readonly "UpdatedAt": string;
}

export interface SessionDomainContextSeed {
  readonly "artifacts"?: ReadonlyArray<SessionDomainContextSeedArtifact> | null;
  readonly "body": string;
  readonly "id": string;
  readonly "provenance": SessionDomainContextSeedProvenance;
  readonly "references"?: ReadonlyArray<SessionDomainContextSeedReference> | null;
  readonly "source": SessionDomainHistorySnapshot;
  readonly "version": number;
}

export interface SessionDomainContextSeedArtifact {
  readonly "id": string;
  readonly "label"?: string;
}

export interface SessionDomainContextSeedProvenance {
  readonly "created_at": string;
  readonly "producer": string;
  readonly "source_model"?: string;
}

export interface SessionDomainContextSeedReference {
  readonly "history": SessionDomainHistoryRef;
  readonly "id": string;
  readonly "label"?: string;
}

export interface SessionDomainControl {
  readonly "manager_id": string;
  readonly "revision": number;
  readonly "session_id": string;
  readonly "state": string;
}

export interface SessionDomainDriverBinding {
  readonly "contract_version": number;
  readonly "driver_id": string;
  readonly "driver_version": string;
  readonly "profile": string;
  readonly "provider_id": string;
}

export interface SessionDomainDriverCheckpointRecord {
  readonly "ContractVersion": number;
  readonly "DriverID": string;
  readonly "DriverVersion": string;
  readonly "State": unknown;
  readonly "UpdatedAt": string;
}

export type SessionDomainFailureCategory = string;

export type SessionDomainFailureOrigin = string;

export interface SessionDomainForkMetadata {
  readonly "ForkedFromID": string;
  readonly "ForkedFromItemID": string;
  readonly "ForkedFromTurnID": string;
}

export interface SessionDomainHeldUserWork {
  readonly "DeliveryUnconfirmed": boolean;
  readonly "ID": string;
  readonly "MessageJSON": string;
  readonly "Origin": string;
  readonly "Position": number;
  readonly "RuntimeJSON": string;
  readonly "SessionID": string;
}

export interface SessionDomainHistoryCheckpoint {
  readonly "created_at": string;
  readonly "kind": string;
  readonly "replacement": ReadonlyArray<SessionDomainHistoryRecord> | null;
  readonly "session_id": string;
  readonly "through_seq": number;
  readonly "version": number;
}

export interface SessionDomainHistoryCursor {
  readonly "field"?: string;
  readonly "offset"?: number;
  readonly "seq": number;
  readonly "session_id": string;
  readonly "snapshot_seq": number;
}

export interface SessionDomainHistoryPage {
  readonly "BudgetExhausted": boolean;
  readonly "HasMore": boolean;
  readonly "HeadSeq": number;
  readonly "Next": (SessionDomainHistoryCursor | null);
  readonly "Records": ReadonlyArray<SessionDomainHistoryRecord> | null;
  readonly "SnapshotSeq": number;
}

export interface SessionDomainHistoryReadQuery {
  readonly "Cursor": (SessionDomainHistoryCursor | null);
  readonly "EndSeq": number;
  readonly "Limit": number;
  readonly "SessionID": string;
  readonly "SnapshotSeq": number;
  readonly "StartSeq": number;
}

export interface SessionDomainHistoryRecord {
  readonly "answer_ready_at"?: (string | null);
  readonly "at"?: string;
  readonly "cache_creation_tokens"?: number;
  readonly "cache_read_tokens"?: number;
  readonly "cause"?: string;
  readonly "client_id"?: string;
  readonly "content": string;
  readonly "content_parts"?: unknown;
  readonly "context_tokens"?: number;
  readonly "discovered_tools"?: unknown;
  readonly "display_content"?: string;
  readonly "engine_turn_id"?: string;
  readonly "engine_turn_pending"?: boolean;
  /** Base64 admission-only compare-and-swap fence; never persist this field into history. */
  readonly "expected_held_message_bytes"?: string;
  readonly "files"?: unknown;
  readonly "finish_reason"?: string;
  readonly "hidden"?: boolean;
  readonly "images"?: unknown;
  readonly "input_tokens"?: number;
  readonly "max_retries"?: number;
  readonly "model"?: string;
  readonly "name"?: string;
  readonly "origin"?: string;
  readonly "origin_id"?: string;
  readonly "output_tokens"?: number;
  readonly "phase"?: string;
  readonly "presentation_kind"?: string;
  readonly "provider"?: string;
  readonly "provider_item_id"?: string;
  readonly "provider_item_model"?: string;
  readonly "provider_items"?: unknown;
  readonly "read_only"?: boolean;
  readonly "reasoning_blocks"?: unknown;
  readonly "reasoning_content"?: string;
  readonly "related_session_id"?: string;
  readonly "retry_count"?: number;
  readonly "role": string;
  readonly "seq"?: number;
  readonly "started_at"?: (string | null);
  readonly "steered"?: boolean;
  readonly "stop_reason"?: string;
  readonly "tool_call_id"?: string;
  readonly "tool_calls"?: unknown;
  readonly "tool_invocation_id"?: string;
  readonly "tool_result"?: unknown;
  readonly "tool_result_kind"?: string;
  readonly "truncated"?: boolean;
}

export interface SessionDomainHistoryRef {
  readonly "end_seq": number;
  readonly "snapshot": SessionDomainHistorySnapshot;
  readonly "start_seq": number;
}

export interface SessionDomainHistorySearchQuery {
  readonly "BeforeSeq": number;
  readonly "Cursor": (SessionDomainHistoryCursor | null);
  readonly "EndSeq": number;
  readonly "Limit": number;
  readonly "Query": string;
  readonly "SessionID": string;
  readonly "SnapshotSeq": number;
  readonly "StartSeq": number;
}

export interface SessionDomainHistorySnapshot {
  readonly "session_id": string;
  readonly "through_seq": number;
}

export interface SessionDomainInboxMessage {
  readonly "Cause": string;
  readonly "ClientID": string;
  readonly "Content": string;
  readonly "Controls": ReadonlyArray<SessionDomainControl> | null;
  readonly "CreatedAt": string;
  readonly "RelatedSessionID": string;
  readonly "SessionID": string;
  readonly "Wake": boolean;
  readonly "WorkID": string;
  readonly "WorkRevision": number;
  readonly "WorkVersion": number;
}

export interface SessionDomainInferenceAttemptJournalRecord {
  readonly "At": string;
  readonly "AttemptID": string;
  readonly "OperationID": string;
  readonly "Ordinal": number;
  readonly "RequestHash": string;
  readonly "WorkflowID": string;
}

export interface SessionDomainInferenceAttemptTerminalRecord {
  readonly "At": string;
  readonly "AttemptID": string;
  readonly "Failure": SessionDomainInferenceJournalFailure;
  readonly "OperationID": string;
  readonly "Outcome": SessionDomainInferenceTerminalOutcome;
}

export type SessionDomainInferenceCostState = string;

export interface SessionDomainInferenceCrashRecovery {
  readonly "Action": SessionDomainRecoveryActionKind;
  readonly "AttemptID": string;
  readonly "Kind": SessionDomainInferenceOperationKind;
  readonly "OperationID": string;
  readonly "Outcome": SessionDomainInferenceTerminalOutcome;
  readonly "OwnerID": string;
  readonly "PriorOutcome": string;
  readonly "PriorPhase": string;
  readonly "PriorRecovery": SessionDomainRecoveryActionKind;
  readonly "Profile": SessionDomainInferenceWorkloadProfile;
}

export interface SessionDomainInferenceJournalFailure {
  readonly "Category": SessionDomainFailureCategory;
  readonly "Confidence": SessionDomainClassificationConfidence;
  readonly "HTTPStatus": number;
  readonly "Message": string;
  readonly "Origin": SessionDomainFailureOrigin;
  readonly "ProviderCode": string;
  readonly "ProviderFamily": string;
}

export interface SessionDomainInferenceOperation {
  readonly "AttemptLimit": number;
  readonly "ID": string;
  readonly "Kind": SessionDomainInferenceOperationKind;
  readonly "ParentOperationID": string;
  readonly "PayloadVersion": number;
  readonly "WorkflowID": string;
  readonly "WorkloadProfile": SessionDomainInferenceWorkloadProfile;
}

export interface SessionDomainInferenceOperationJournalRecord {
  readonly "At": string;
  readonly "Operation": SessionDomainInferenceOperation;
  readonly "RequestHash": string;
  readonly "Workflow": SessionDomainInferenceWorkflowJournalRecord;
}

export type SessionDomainInferenceOperationKind = string;

export interface SessionDomainInferenceOperationTerminalRecord {
  readonly "At": string;
  readonly "Failure": SessionDomainInferenceJournalFailure;
  readonly "OperationID": string;
  readonly "Outcome": SessionDomainInferenceTerminalOutcome;
}

export interface SessionDomainInferenceRecoveryAttemptJournalRecord {
  readonly "NextAttempt": SessionDomainInferenceAttemptJournalRecord;
  readonly "Recovery": SessionDomainInferenceRecoveryJournalRecord;
}

export interface SessionDomainInferenceRecoveryJournalRecord {
  readonly "Action": SessionDomainRecoveryActionKind;
  readonly "At": string;
  readonly "AttemptID": string;
  readonly "OperationID": string;
  readonly "RetryAt": string;
}

export interface SessionDomainInferenceSubmissionJournalRecord {
  readonly "AttemptID": string;
  readonly "AttemptOrdinal": number;
  readonly "CompletedAt": string;
  readonly "CostState": SessionDomainInferenceCostState;
  readonly "EstimatedUsage": (SessionDomainTokenUsage | null);
  readonly "FailureCategory": SessionDomainFailureCategory;
  readonly "ID": string;
  readonly "Mode": string;
  readonly "OperationID": string;
  readonly "Ordinal": number;
  readonly "Outcome": SessionDomainInferenceSubmissionOutcome;
  readonly "OutputBytes": number;
  readonly "Protocol": string;
  readonly "Provider": string;
  readonly "Reason": string;
  readonly "ReportedUsage": (SessionDomainTokenUsage | null);
  readonly "StartedAt": string;
  readonly "Transport": string;
}

export type SessionDomainInferenceSubmissionOutcome = string;

export type SessionDomainInferenceTerminalOutcome = string;

export interface SessionDomainInferenceWorkflowJournalRecord {
  readonly "Budget": SessionDomainWorkflowBudgetSpec;
  readonly "ID": string;
  readonly "Profile": SessionDomainInferenceWorkloadProfile;
  readonly "StartedAt": string;
}

export interface SessionDomainInferenceWorkflowTerminalRecord {
  readonly "At": string;
  readonly "Outcome": SessionDomainInferenceTerminalOutcome;
  readonly "WorkflowID": string;
}

export type SessionDomainInferenceWorkloadProfile = string;

export type SessionDomainKind = string;

export interface SessionDomainManagedMetadata {
  readonly "ContextSource": string;
  readonly "CreationRequestID": string;
  readonly "Owner": string;
  readonly "ParentID": string;
  readonly "Visibility": string;
}

export interface SessionDomainModelInputReceiptMaintenanceResult {
  readonly "BytesAfter": number;
  readonly "BytesBefore": number;
  readonly "Compressed": number;
  readonly "CompressionDone": boolean;
  readonly "Deleted": number;
}

export interface SessionDomainModelInputReceiptRecord {
  readonly "ContractVersion": number;
  readonly "CreatedAt": string;
  readonly "DriverID": string;
  readonly "DriverVersion": string;
  readonly "OperationID": string;
  /** Base64 of exact admitted JSON bytes; never parse/re-encode for receipt equality. */
  readonly "Payload": string;
}

export interface SessionDomainOrganizationGroup {
  readonly "created_at": string;
  readonly "id": string;
  readonly "name": string;
  readonly "sort_order": number;
  readonly "updated_at": string;
}

export interface SessionDomainParticipant {
  readonly "Avatar": string;
  readonly "CreatedAt": string;
  readonly "ID": string;
  readonly "Kind": SessionDomainKind;
  readonly "Model": string;
  readonly "Name": string;
  readonly "RetiredAt": (string | null);
  readonly "Role": string;
  readonly "Tagline": string;
  readonly "UpdatedAt": string;
  readonly "Workspace": string;
}

export interface SessionDomainParticipantRun {
  readonly "AgentID": string;
  readonly "CreatedAt": string;
  readonly "ID": string;
  readonly "Outcome": string;
  readonly "ParticipantID": string;
  readonly "SessionID": string;
  readonly "Summary": string;
  readonly "TaskID": string;
  readonly "UpdatedAt": string;
}

export interface SessionDomainPinnedArchived {
  readonly "Archived": boolean;
  readonly "Pinned": boolean;
}

export interface SessionDomainPluginGenerationBinding {
  readonly "fingerprint": string;
  readonly "id": string;
}

export interface SessionDomainPluginGenerationSnapshot {
  readonly "plugins": ReadonlyArray<SessionDomainPluginGenerationBinding> | null;
}

export interface SessionDomainPluginTurnLifecycleEntry {
  readonly "Payload": unknown;
  readonly "PluginID": string;
  readonly "QueueID": string;
  readonly "RequestID": string;
  readonly "SessionID": string;
  readonly "TurnID": string;
  readonly "UpdatedAt": string;
}

export interface SessionDomainPluginTurnLifecycleOutboxEntry {
  readonly "Payload": unknown;
  readonly "PluginID": string;
  readonly "RequestID": string;
  readonly "UpdatedAt": string;
}

export interface SessionDomainProjectWork {
  readonly "acceptance": string;
  readonly "authority": string;
  readonly "blocker"?: string;
  readonly "brief": string;
  readonly "code_ref"?: string;
  readonly "created_at": string;
  readonly "delivery"?: string;
  readonly "delivery_by"?: string;
  readonly "evidence"?: string;
  readonly "executor_id"?: string;
  readonly "executor_input"?: (SessionDomainProjectWorkInput | null);
  readonly "executor_model": SessionDomainRuntimeSelection;
  readonly "id": string;
  readonly "lead_id": string;
  readonly "lead_input"?: (SessionDomainProjectWorkInput | null);
  readonly "lead_model": SessionDomainRuntimeSelection;
  readonly "phase": string;
  readonly "project_id": string;
  readonly "review"?: string;
  readonly "review_turn_id"?: string;
  readonly "revision": number;
  readonly "source_refs"?: string;
  readonly "submission_turn_id"?: string;
  readonly "summary"?: string;
  readonly "title": string;
  readonly "updated_at": string;
  readonly "version": number;
  readonly "workspace": string;
}

export interface SessionDomainProjectWorkInput {
  readonly "brief": string;
  readonly "client_id": string;
  readonly "revision": number;
  readonly "sender_id": string;
}

export interface SessionDomainProviderHistorySnapshot {
  readonly "Checkpoint": (SessionDomainHistoryCheckpoint | null);
  readonly "HeadSeq": number;
  readonly "Records": ReadonlyArray<SessionDomainHistoryRecord> | null;
}

export type SessionDomainRecoveryActionKind = string;

export interface SessionDomainRedundantStorageMaintenanceResult {
  readonly "CheckpointBytesAfter": number;
  readonly "CheckpointBytesBefore": number;
  readonly "CheckpointsEncoded": number;
  readonly "ProjectedToolResultsCleared": number;
  readonly "SupersededCheckpointsDeleted": number;
  readonly "ToolMessageContentsCompacted": number;
}

export interface SessionDomainRuntimeSelection {
  readonly "ApproveForMe": boolean;
  readonly "Effort": string;
  readonly "Model": string;
  readonly "ModelOptions": Readonly<Record<string, string>> | null;
  readonly "PermissionMode": string;
  readonly "Provider": string;
  readonly "Speed": string;
  readonly "Variant": string;
}

export interface SessionDomainSession {
  readonly "approve_for_me"?: boolean;
  readonly "archive_reason"?: string;
  readonly "archived_at"?: (string | null);
  readonly "context_source"?: string;
  readonly "created_at": string;
  readonly "creation_request_id"?: string;
  readonly "cwd"?: string;
  readonly "effort"?: string;
  readonly "engine_id"?: string;
  readonly "engine_ref"?: string;
  readonly "entries": number;
  readonly "folder_id"?: string;
  readonly "forked_from_id"?: string;
  readonly "forked_from_item_id"?: string;
  readonly "forked_from_turn_id"?: string;
  readonly "id": string;
  readonly "instructions"?: string;
  readonly "latest_completed_turn_id"?: string;
  readonly "model"?: string;
  readonly "model_options"?: Readonly<Record<string, string>> | null;
  readonly "owner"?: string;
  readonly "parent_id"?: string;
  readonly "permission_mode"?: string;
  readonly "pinned_at"?: (string | null);
  readonly "project_role"?: string;
  readonly "provider"?: string;
  readonly "seed_id"?: string;
  readonly "source"?: string;
  readonly "speed"?: string;
  readonly "summary"?: string;
  readonly "title"?: string;
  readonly "title_revision"?: number;
  readonly "tool_policy_json"?: string;
  readonly "updated_at"?: string;
  readonly "variant"?: string;
  readonly "visibility"?: string;
  readonly "workspace_id"?: string;
  readonly "worktree_base_head"?: string;
  readonly "worktree_base_repo"?: string;
  readonly "worktree_path"?: string;
}

export interface SessionDomainSessionLaunchInput {
  readonly "context_json"?: string;
  readonly "intent"?: string;
  readonly "prompt"?: string;
}

export interface SessionDomainSessionLaunchRecord {
  readonly "CommittedAt": (string | null);
  readonly "CreatedAt": string;
  readonly "Error": string;
  readonly "InitialTurnID": string;
  readonly "Input": SessionDomainSessionLaunchInput;
  readonly "Kind": string;
  readonly "Owner": string;
  readonly "Producer": string;
  readonly "RequestID": string;
  readonly "Revision": number;
  readonly "Runtime": SessionDomainSessionRuntimeSelection;
  readonly "SeedID": string;
  readonly "SourceCutoff": number;
  readonly "SourceSession": string;
  readonly "Status": string;
  readonly "TargetSession": string;
  readonly "UpdatedAt": string;
}

export interface SessionDomainSessionListCursor {
  readonly "key": string;
  readonly "revision": string;
}

export interface SessionDomainSessionListPage {
  readonly "Next": (SessionDomainSessionListCursor | null);
  readonly "Sessions": ReadonlyArray<SessionDomainSession> | null;
}

export interface SessionDomainSessionListQuery {
  readonly "CWD": string;
  readonly "Cursor": (SessionDomainSessionListCursor | null);
  readonly "Limit": number;
  readonly "WorkspaceID": string;
}

export interface SessionDomainSessionOrganization {
  readonly "folders": ReadonlyArray<SessionDomainOrganizationGroup> | null;
}

export interface SessionDomainSessionRuntimeSelection {
  readonly "effort"?: string;
  readonly "model": string;
  readonly "model_options"?: Readonly<Record<string, string>> | null;
  readonly "permission_mode"?: string;
  readonly "provider": string;
  readonly "speed"?: string;
  readonly "variant"?: string;
}

export interface SessionDomainSubscriptionActivity {
  readonly "At": string;
  readonly "CacheCreationTokens": number;
  readonly "CacheReadTokens": number;
  readonly "Error": string;
  readonly "InputTokens": number;
  readonly "Key": SessionDomainSubscriptionActivityKey;
  readonly "LocalUsage": SessionDomainSubscriptionUsage;
  readonly "Model": string;
  readonly "OutputTokens": number;
  readonly "Status": string;
  readonly "UsageReported": boolean;
}

export interface SessionDomainSubscriptionActivityKey {
  readonly "EngineID": string;
  readonly "Provider": string;
}

export interface SessionDomainSubscriptionUsage {
  readonly "cache_creation_tokens": number;
  readonly "cache_read_tokens": number;
  readonly "input_tokens": number;
  readonly "output_tokens": number;
  readonly "reported_turns": number;
}

export interface SessionDomainTokenUsage {
  readonly "CacheCreationTokens": number;
  readonly "CacheCreationUnknown": boolean;
  readonly "CacheReadTokens": number;
  readonly "InputTokens": number;
  readonly "OutputTokens": number;
}

export interface SessionDomainTokenUsageRow {
  readonly "At": string;
  readonly "CacheCreationTokens": number;
  readonly "CacheReadTokens": number;
  readonly "InputTokens": number;
  readonly "Model": string;
  readonly "OutputTokens": number;
  readonly "Provider": string;
  readonly "SessionID": string;
}

export interface SessionDomainWorkflowBudgetSpec {
  readonly "MaxAttempts": SessionDomainBudgetLimit;
  readonly "MaxChildOperations": SessionDomainBudgetLimit;
  readonly "MaxCredentialRefreshes": SessionDomainBudgetLimit;
  readonly "MaxOperations": SessionDomainBudgetLimit;
  readonly "MaxPayloadTransforms": SessionDomainBudgetLimit;
  readonly "MaxRecoveryWaitMillis": SessionDomainBudgetLimit;
  readonly "MaxSamePayloadReplays": SessionDomainBudgetLimit;
  readonly "MaxSubmissions": SessionDomainBudgetLimit;
  readonly "MaxTransportSwitches": SessionDomainBudgetLimit;
  readonly "MaxUnknownBillableSubmissions": SessionDomainBudgetLimit;
  readonly "MaxUsageTokens": SessionDomainBudgetLimit;
}

export interface SessionDomainWorktreeInfo {
  readonly "BaseHEAD": string;
  readonly "BaseRepo": string;
  readonly "Path": string;
}

/** Methods accept positional tuples and return tuples excluding Go error returns.
 * Each method is one domain transaction unless documented as resource lifecycle.
 * Journal methods start with a caller-owned handle; journal writes also take an owner ID.
 * Times are RFC 3339 strings. Durable document revisions are decimal strings.
 * An uncertain mutation response is not permission to blindly retry a domain mutation.
 */
export interface SessionDomainMethods {
  "AcceptHeldUserWork": { args: readonly [string, SessionDomainHeldUserWork]; values: readonly [boolean]; };
  "AdmitThreadExecution": { args: readonly [string, number, ReadonlyArray<string> | null, ReadonlyArray<string> | null]; values: readonly [number]; };
  "AppendAcceptedUserHistoryRecord": { args: readonly [string, SessionDomainHistoryRecord, SessionDomainHeldUserWork]; values: readonly [number]; };
  "AppendContinuationHistoryRecord": { args: readonly [string, SessionDomainHistoryRecord, (SessionDomainControl | null), SessionDomainControl]; values: readonly [number]; };
  "AppendControlledHistoryRecord": { args: readonly [string, SessionDomainHistoryRecord, (SessionDomainControl | null)]; values: readonly [number]; };
  "AppendHistoryRecord": { args: readonly [string, SessionDomainHistoryRecord]; values: readonly []; };
  "AppendHistoryRecordReturningSeq": { args: readonly [string, SessionDomainHistoryRecord]; values: readonly [number]; };
  "AppendHistoryRecords": { args: readonly [string, ReadonlyArray<SessionDomainHistoryRecord> | null]; values: readonly []; };
  "AppendHistoryRecordsReturningRange": { args: readonly [string, ReadonlyArray<SessionDomainHistoryRecord> | null]; values: readonly [number, number]; };
  "BindDriver": { args: readonly [string, SessionDomainDriverBinding]; values: readonly []; };
  "BindWorktree": { args: readonly [string, SessionDomainWorktreeInfo]; values: readonly [SessionDomainSession]; };
  "CancelHeldUserWork": { args: readonly [string, string, string]; values: readonly [boolean]; };
  "ChangeControl": { args: readonly [string, string, string, number]; values: readonly [SessionDomainControl]; };
  "ClaimHeldUserWork": { args: readonly [string, SessionDomainHeldUserWork]; values: readonly []; };
  "CompareAndSwapCompactionNote": { args: readonly [SessionDomainCompactionNote, boolean, SessionDomainCompactionNote]; values: readonly [boolean]; };
  "CompleteParticipantRun": { args: readonly [string, string, string, string]; values: readonly []; };
  "CountParticipantsByKind": { args: readonly [SessionDomainKind]; values: readonly [number]; };
  "Create": { args: readonly [ReadonlyArray<string> | null]; values: readonly [(SessionDomainSession | null)]; };
  "CreateFolder": { args: readonly [string]; values: readonly [SessionDomainOrganizationGroup]; };
  "CreateForkWithMetadata": { args: readonly [string, string, SessionDomainForkMetadata]; values: readonly [(SessionDomainSession | null)]; };
  "CreateInitialized": { args: readonly [SessionDomainSession, ReadonlyArray<SessionDomainHistoryRecord> | null]; values: readonly [(SessionDomainSession | null)]; };
  "CreateInitializedWithInbox": { args: readonly [SessionDomainSession, ReadonlyArray<SessionDomainHistoryRecord> | null, SessionDomainContextSeed, SessionDomainSessionLaunchRecord, SessionDomainInboxMessage]; values: readonly [(SessionDomainSession | null)]; };
  "CreateInitializedWithLaunch": { args: readonly [SessionDomainSession, ReadonlyArray<SessionDomainHistoryRecord> | null, SessionDomainContextSeed, SessionDomainSessionLaunchRecord]; values: readonly [(SessionDomainSession | null)]; };
  "CreateManagedForkWithMetadata": { args: readonly [string, string, SessionDomainForkMetadata, SessionDomainManagedMetadata]; values: readonly [(SessionDomainSession | null)]; };
  "CreateManagedWithMetadata": { args: readonly [string, string, SessionDomainManagedMetadata]; values: readonly [(SessionDomainSession | null)]; };
  "CreateManagedWithWorktree": { args: readonly [string, string, SessionDomainForkMetadata, SessionDomainWorktreeInfo, SessionDomainManagedMetadata]; values: readonly [(SessionDomainSession | null)]; };
  "CreateWithMetadata": { args: readonly [string, string]; values: readonly [(SessionDomainSession | null)]; };
  "CreateWithWorktree": { args: readonly [string, string, SessionDomainForkMetadata, SessionDomainWorktreeInfo]; values: readonly [(SessionDomainSession | null)]; };
  "Delete": { args: readonly [string]; values: readonly [SessionDomainSession]; };
  "DeleteArchived": { args: readonly [string]; values: readonly [SessionDomainSession]; };
  "DeleteFolder": { args: readonly [string]; values: readonly []; };
  "DeleteHeldUserWork": { args: readonly [string, string]; values: readonly [boolean]; };
  "DeletePluginTurnLifecycleOutbox": { args: readonly [string, string]; values: readonly []; };
  "DeletePluginTurnLifecycleOutboxForPlugin": { args: readonly [string]; values: readonly []; };
  "EnqueueInbox": { args: readonly [SessionDomainInboxMessage]; values: readonly []; };
  "Find": { args: readonly [string]; values: readonly [SessionDomainSession, boolean]; };
  "FindManagedByRequest": { args: readonly [string, string]; values: readonly [SessionDomainSession, boolean]; };
  "FindPinnedArchived": { args: readonly [ReadonlyArray<string> | null]; values: readonly [Readonly<Record<string, SessionDomainPinnedArchived>> | null]; };
  "FindPluginTurnLifecycle": { args: readonly [string, string, string, string]; values: readonly [SessionDomainPluginTurnLifecycleEntry, boolean]; };
  "FindRetiredParticipantByName": { args: readonly [SessionDomainKind, string]; values: readonly [SessionDomainParticipant, boolean]; };
  "GetParticipant": { args: readonly [string]; values: readonly [SessionDomainParticipant]; };
  "HasHeldUserWorkReceipt": { args: readonly [string, string]; values: readonly [boolean]; };
  "InboxHas": { args: readonly [string]; values: readonly [boolean]; };
  "InboxTargets": { args: readonly []; values: readonly [ReadonlyArray<string> | null]; };
  "Journal.Close": { args: readonly [string]; values: readonly []; };
  "Journal.CompleteAttempt": { args: readonly [string, string, SessionDomainInferenceAttemptTerminalRecord]; values: readonly []; };
  "Journal.CompleteOperation": { args: readonly [string, string, SessionDomainInferenceOperationTerminalRecord]; values: readonly []; };
  "Journal.CompleteWorkflow": { args: readonly [string, string, SessionDomainInferenceWorkflowTerminalRecord]; values: readonly []; };
  "Journal.MarkAttemptFirstEvent": { args: readonly [string, string, string, string, string, string]; values: readonly []; };
  "Journal.PrepareAttempt": { args: readonly [string, string, SessionDomainInferenceAttemptJournalRecord]; values: readonly []; };
  "Journal.PrepareOperation": { args: readonly [string, string, SessionDomainInferenceOperationJournalRecord]; values: readonly []; };
  "Journal.PrepareRecoveryAttempt": { args: readonly [string, string, SessionDomainInferenceRecoveryAttemptJournalRecord]; values: readonly []; };
  "Journal.Prune": { args: readonly [string, string]; values: readonly [number]; };
  "Journal.ReconcileOrphans": { args: readonly [string, string]; values: readonly [ReadonlyArray<SessionDomainInferenceCrashRecovery> | null]; };
  "Journal.UpsertSubmission": { args: readonly [string, string, SessionDomainInferenceSubmissionJournalRecord]; values: readonly []; };
  "LatestHistoryCheckpoint": { args: readonly [string]; values: readonly [SessionDomainHistoryCheckpoint, boolean]; };
  "LatestSubscriptionActivity": { args: readonly [ReadonlyArray<SessionDomainSubscriptionActivityKey> | null]; values: readonly [ReadonlyArray<SessionDomainSubscriptionActivity> | null]; };
  "List": { args: readonly [number]; values: readonly [ReadonlyArray<SessionDomainSession> | null]; };
  "ListControls": { args: readonly []; values: readonly [Readonly<Record<string, SessionDomainControl>> | null]; };
  "ListForCWD": { args: readonly [string, string, number]; values: readonly [ReadonlyArray<SessionDomainSession> | null]; };
  "ListOrganization": { args: readonly []; values: readonly [SessionDomainSessionOrganization]; };
  "ListPage": { args: readonly [SessionDomainSessionListQuery]; values: readonly [SessionDomainSessionListPage]; };
  "ListParticipantRuns": { args: readonly [string, number]; values: readonly [ReadonlyArray<SessionDomainParticipantRun> | null]; };
  "ListParticipants": { args: readonly [SessionDomainKind]; values: readonly [ReadonlyArray<SessionDomainParticipant> | null]; };
  "ListPluginTurnLifecycleOutbox": { args: readonly []; values: readonly [ReadonlyArray<SessionDomainPluginTurnLifecycleOutboxEntry> | null]; };
  "ListProjectWork": { args: readonly [string]; values: readonly [ReadonlyArray<SessionDomainProjectWork> | null]; };
  "ListTokenUsage": { args: readonly []; values: readonly [ReadonlyArray<SessionDomainTokenUsageRow> | null]; };
  "LoadActiveHistoryRecords": { args: readonly [string, boolean]; values: readonly [ReadonlyArray<SessionDomainHistoryRecord> | null]; };
  "LoadCompactionNote": { args: readonly [string, string]; values: readonly [SessionDomainCompactionNote, boolean]; };
  "LoadContextSeed": { args: readonly [string]; values: readonly [SessionDomainContextSeed, boolean]; };
  "LoadDriverBinding": { args: readonly [string]; values: readonly [SessionDomainDriverBinding, boolean]; };
  "LoadDriverCheckpoint": { args: readonly [string]; values: readonly [SessionDomainDriverCheckpointRecord, boolean]; };
  "LoadHeldUserWork": { args: readonly [string]; values: readonly [ReadonlyArray<SessionDomainHeldUserWork> | null]; };
  "LoadHistoryRecords": { args: readonly [string, boolean]; values: readonly [ReadonlyArray<SessionDomainHistoryRecord> | null]; };
  "LoadModelInputReceipts": { args: readonly [string]; values: readonly [ReadonlyArray<SessionDomainModelInputReceiptRecord> | null]; };
  "LoadProviderHistorySnapshot": { args: readonly [string]; values: readonly [SessionDomainProviderHistorySnapshot]; };
  "LoadRequestContextCheckpoint": { args: readonly [string]; values: readonly [unknown]; };
  "LoadSessionLaunch": { args: readonly [string]; values: readonly [SessionDomainSessionLaunchRecord, boolean]; };
  "MaintainModelInputReceipts": { args: readonly [string]; values: readonly [SessionDomainModelInputReceiptMaintenanceResult]; };
  "MaintainRedundantStorage": { args: readonly []; values: readonly [SessionDomainRedundantStorageMaintenanceResult]; };
  "MostRecent": { args: readonly []; values: readonly [string]; };
  "MostRecentForCWD": { args: readonly [string, string]; values: readonly [string]; };
  "NewInferenceJournalRuntime": { args: readonly [string]; values: readonly [SessionDomainJournalHandle]; };
  "PendingInbox": { args: readonly [string]; values: readonly [ReadonlyArray<SessionDomainInboxMessage> | null]; };
  "PendingProjectWork": { args: readonly [string]; values: readonly [ReadonlyArray<SessionDomainProjectWork> | null]; };
  "ProjectWorkOperation": { args: readonly [string]; values: readonly [SessionDomainProjectWork, boolean]; };
  "PutContextSeed": { args: readonly [SessionDomainContextSeed]; values: readonly [SessionDomainContextSeed]; };
  "PutPluginTurnLifecycleOutbox": { args: readonly [string, string, unknown]; values: readonly []; };
  "PutSessionLaunch": { args: readonly [SessionDomainSessionLaunchRecord]; values: readonly [SessionDomainSessionLaunchRecord]; };
  "ReadControl": { args: readonly [string]; values: readonly [SessionDomainControl, boolean]; };
  "ReadHistoryPage": { args: readonly [string, number, number]; values: readonly [SessionDomainHistoryPage]; };
  "ReadHistoryQuery": { args: readonly [SessionDomainHistoryReadQuery]; values: readonly [SessionDomainHistoryPage]; };
  "ReadPluginGenerationSnapshot": { args: readonly [string]; values: readonly [SessionDomainPluginGenerationSnapshot, boolean]; };
  "ReadProjectWork": { args: readonly [string]; values: readonly [SessionDomainProjectWork]; };
  "RebuildSessionIndexes": { args: readonly []; values: readonly []; };
  "ReconcileProjectWork": { args: readonly [string, number]; values: readonly []; };
  "ReleaseHeldUserWork": { args: readonly [string, string]; values: readonly []; };
  "RenameFolder": { args: readonly [string, string]; values: readonly [SessionDomainOrganizationGroup]; };
  "ReorderFolders": { args: readonly [ReadonlyArray<string> | null]; values: readonly []; };
  "ReplaceHeldUserWork": { args: readonly [string, ReadonlyArray<SessionDomainHeldUserWork> | null]; values: readonly []; };
  "RequestThreadExecutionResetRecord": { args: readonly [string]; values: readonly []; };
  "RetireParticipant": { args: readonly [string]; values: readonly []; };
  "RewriteHistoryRecords": { args: readonly [string, ReadonlyArray<SessionDomainHistoryRecord> | null]; values: readonly []; };
  "RewriteHistoryRecordsAtBaseline": { args: readonly [string, ReadonlyArray<SessionDomainHistoryRecord> | null, number]; values: readonly []; };
  "RewriteHistoryRecordsForEdit": { args: readonly [string, ReadonlyArray<SessionDomainHistoryRecord> | null, number, number]; values: readonly []; };
  "RewriteHistoryRecordsForEngineEdit": { args: readonly [string, ReadonlyArray<SessionDomainHistoryRecord> | null, number, number, string, string]; values: readonly []; };
  "SaveDriverCheckpoint": { args: readonly [string, SessionDomainDriverCheckpointRecord]; values: readonly []; };
  "SaveModelInputReceipt": { args: readonly [string, SessionDomainModelInputReceiptRecord]; values: readonly []; };
  "SaveProjectWork": { args: readonly [SessionDomainProjectWork, number, string]; values: readonly [SessionDomainProjectWork]; };
  "SaveRequestContextCheckpoint": { args: readonly [string, unknown]; values: readonly []; };
  "SearchHistoryPage": { args: readonly [string, string, number, number]; values: readonly [SessionDomainHistoryPage]; };
  "SearchHistoryQuery": { args: readonly [SessionDomainHistorySearchQuery]; values: readonly [SessionDomainHistoryPage]; };
  "SetEngine": { args: readonly [string, string]; values: readonly [SessionDomainSession]; };
  "SetEngineRef": { args: readonly [string, string]; values: readonly [SessionDomainSession]; };
  "SetHistoryEngineTurnID": { args: readonly [string, number, string, string, boolean]; values: readonly []; };
  "SetModelSelection": { args: readonly [string, string, string, string]; values: readonly [SessionDomainSession]; };
  "SetProjectMembership": { args: readonly [string, string, string, string]; values: readonly [SessionDomainSession]; };
  "SetRuntimeSelection": { args: readonly [string, SessionDomainRuntimeSelection]; values: readonly [SessionDomainSession]; };
  "SetSource": { args: readonly [string, string]; values: readonly [SessionDomainSession]; };
  "SetWorkspaceID": { args: readonly [string, string]; values: readonly [SessionDomainSession]; };
  "SettleInbox": { args: readonly [string, string]; values: readonly []; };
  "StoreCompactionNote": { args: readonly [SessionDomainCompactionNote]; values: readonly []; };
  "StoreContextWindow": { args: readonly [string, ReadonlyArray<SessionDomainHistoryRecord> | null, number, SessionDomainCompactionNote]; values: readonly [SessionDomainHistoryCheckpoint]; };
  "StoreHistoryCheckpointAtBaseline": { args: readonly [string, string, ReadonlyArray<SessionDomainHistoryRecord> | null, number]; values: readonly [SessionDomainHistoryCheckpoint]; };
  "ThreadExecutionResetRequested": { args: readonly [string, number]; values: readonly [boolean]; };
  "UpdateArchived": { args: readonly [string, boolean]; values: readonly [SessionDomainSession]; };
  "UpdateGeneratedTitle": { args: readonly [string, string]; values: readonly [SessionDomainSession]; };
  "UpdateHeldUserWork": { args: readonly [string, SessionDomainHeldUserWork, SessionDomainHeldUserWork]; values: readonly [boolean]; };
  "UpdateIndex": { args: readonly [string, number, string]; values: readonly []; };
  "UpdateOrganization": { args: readonly [string, (string | null)]; values: readonly [SessionDomainSession]; };
  "UpdatePinned": { args: readonly [string, boolean]; values: readonly [SessionDomainSession]; };
  "UpdateTitle": { args: readonly [string, string]; values: readonly [SessionDomainSession]; };
  "UpdateWorkspaceBinding": { args: readonly [string, string, string, string, string]; values: readonly [SessionDomainSession]; };
  "UpsertParticipant": { args: readonly [SessionDomainParticipant]; values: readonly []; };
  "UpsertParticipantRun": { args: readonly [SessionDomainParticipantRun]; values: readonly []; };
  "ValidateContinuationControl": { args: readonly [SessionDomainControl]; values: readonly []; };
  "ValidateControl": { args: readonly [SessionDomainControl]; values: readonly []; };
  "ValidateInboxControls": { args: readonly [string]; values: readonly []; };
  "WorktreeInfoForSession": { args: readonly [string]; values: readonly [SessionDomainWorktreeInfo, boolean]; };
  "WritePluginGenerationSnapshot": { args: readonly [string, SessionDomainPluginGenerationSnapshot]; values: readonly []; };
}

const sessionDomainArity = {
  "AcceptHeldUserWork": [2, 1],
  "AdmitThreadExecution": [4, 1],
  "AppendAcceptedUserHistoryRecord": [3, 1],
  "AppendContinuationHistoryRecord": [4, 1],
  "AppendControlledHistoryRecord": [3, 1],
  "AppendHistoryRecord": [2, 0],
  "AppendHistoryRecordReturningSeq": [2, 1],
  "AppendHistoryRecords": [2, 0],
  "AppendHistoryRecordsReturningRange": [2, 2],
  "BindDriver": [2, 0],
  "BindWorktree": [2, 1],
  "CancelHeldUserWork": [3, 1],
  "ChangeControl": [4, 1],
  "ClaimHeldUserWork": [2, 0],
  "CompareAndSwapCompactionNote": [3, 1],
  "CompleteParticipantRun": [4, 0],
  "CountParticipantsByKind": [1, 1],
  "Create": [1, 1],
  "CreateFolder": [1, 1],
  "CreateForkWithMetadata": [3, 1],
  "CreateInitialized": [2, 1],
  "CreateInitializedWithInbox": [5, 1],
  "CreateInitializedWithLaunch": [4, 1],
  "CreateManagedForkWithMetadata": [4, 1],
  "CreateManagedWithMetadata": [3, 1],
  "CreateManagedWithWorktree": [5, 1],
  "CreateWithMetadata": [2, 1],
  "CreateWithWorktree": [4, 1],
  "Delete": [1, 1],
  "DeleteArchived": [1, 1],
  "DeleteFolder": [1, 0],
  "DeleteHeldUserWork": [2, 1],
  "DeletePluginTurnLifecycleOutbox": [2, 0],
  "DeletePluginTurnLifecycleOutboxForPlugin": [1, 0],
  "EnqueueInbox": [1, 0],
  "Find": [1, 2],
  "FindManagedByRequest": [2, 2],
  "FindPinnedArchived": [1, 1],
  "FindPluginTurnLifecycle": [4, 2],
  "FindRetiredParticipantByName": [2, 2],
  "GetParticipant": [1, 1],
  "HasHeldUserWorkReceipt": [2, 1],
  "InboxHas": [1, 1],
  "InboxTargets": [0, 1],
  "Journal.Close": [1, 0],
  "Journal.CompleteAttempt": [3, 0],
  "Journal.CompleteOperation": [3, 0],
  "Journal.CompleteWorkflow": [3, 0],
  "Journal.MarkAttemptFirstEvent": [6, 0],
  "Journal.PrepareAttempt": [3, 0],
  "Journal.PrepareOperation": [3, 0],
  "Journal.PrepareRecoveryAttempt": [3, 0],
  "Journal.Prune": [2, 1],
  "Journal.ReconcileOrphans": [2, 1],
  "Journal.UpsertSubmission": [3, 0],
  "LatestHistoryCheckpoint": [1, 2],
  "LatestSubscriptionActivity": [1, 1],
  "List": [1, 1],
  "ListControls": [0, 1],
  "ListForCWD": [3, 1],
  "ListOrganization": [0, 1],
  "ListPage": [1, 1],
  "ListParticipantRuns": [2, 1],
  "ListParticipants": [1, 1],
  "ListPluginTurnLifecycleOutbox": [0, 1],
  "ListProjectWork": [1, 1],
  "ListTokenUsage": [0, 1],
  "LoadActiveHistoryRecords": [2, 1],
  "LoadCompactionNote": [2, 2],
  "LoadContextSeed": [1, 2],
  "LoadDriverBinding": [1, 2],
  "LoadDriverCheckpoint": [1, 2],
  "LoadHeldUserWork": [1, 1],
  "LoadHistoryRecords": [2, 1],
  "LoadModelInputReceipts": [1, 1],
  "LoadProviderHistorySnapshot": [1, 1],
  "LoadRequestContextCheckpoint": [1, 1],
  "LoadSessionLaunch": [1, 2],
  "MaintainModelInputReceipts": [1, 1],
  "MaintainRedundantStorage": [0, 1],
  "MostRecent": [0, 1],
  "MostRecentForCWD": [2, 1],
  "NewInferenceJournalRuntime": [1, 1],
  "PendingInbox": [1, 1],
  "PendingProjectWork": [1, 1],
  "ProjectWorkOperation": [1, 2],
  "PutContextSeed": [1, 1],
  "PutPluginTurnLifecycleOutbox": [3, 0],
  "PutSessionLaunch": [1, 1],
  "ReadControl": [1, 2],
  "ReadHistoryPage": [3, 1],
  "ReadHistoryQuery": [1, 1],
  "ReadPluginGenerationSnapshot": [1, 2],
  "ReadProjectWork": [1, 1],
  "RebuildSessionIndexes": [0, 0],
  "ReconcileProjectWork": [2, 0],
  "ReleaseHeldUserWork": [2, 0],
  "RenameFolder": [2, 1],
  "ReorderFolders": [1, 0],
  "ReplaceHeldUserWork": [2, 0],
  "RequestThreadExecutionResetRecord": [1, 0],
  "RetireParticipant": [1, 0],
  "RewriteHistoryRecords": [2, 0],
  "RewriteHistoryRecordsAtBaseline": [3, 0],
  "RewriteHistoryRecordsForEdit": [4, 0],
  "RewriteHistoryRecordsForEngineEdit": [6, 0],
  "SaveDriverCheckpoint": [2, 0],
  "SaveModelInputReceipt": [2, 0],
  "SaveProjectWork": [3, 1],
  "SaveRequestContextCheckpoint": [2, 0],
  "SearchHistoryPage": [4, 1],
  "SearchHistoryQuery": [1, 1],
  "SetEngine": [2, 1],
  "SetEngineRef": [2, 1],
  "SetHistoryEngineTurnID": [5, 0],
  "SetModelSelection": [4, 1],
  "SetProjectMembership": [4, 1],
  "SetRuntimeSelection": [2, 1],
  "SetSource": [2, 1],
  "SetWorkspaceID": [2, 1],
  "SettleInbox": [2, 0],
  "StoreCompactionNote": [1, 0],
  "StoreContextWindow": [4, 1],
  "StoreHistoryCheckpointAtBaseline": [4, 1],
  "ThreadExecutionResetRequested": [2, 1],
  "UpdateArchived": [2, 1],
  "UpdateGeneratedTitle": [2, 1],
  "UpdateHeldUserWork": [3, 1],
  "UpdateIndex": [3, 0],
  "UpdateOrganization": [2, 1],
  "UpdatePinned": [2, 1],
  "UpdateTitle": [2, 1],
  "UpdateWorkspaceBinding": [5, 1],
  "UpsertParticipant": [1, 0],
  "UpsertParticipantRun": [1, 0],
  "ValidateContinuationControl": [1, 0],
  "ValidateControl": [1, 0],
  "ValidateInboxControls": [1, 0],
  "WorktreeInfoForSession": [1, 2],
  "WritePluginGenerationSnapshot": [2, 0],
} as const;

export type SessionDomainMethod = keyof SessionDomainMethods;
export const SESSION_DOMAIN_METHODS = Object.freeze(Object.keys(sessionDomainArity) as SessionDomainMethod[]);

export function requireSessionDomain(): ServiceRequirement {
  return { name: SESSION_DOMAIN_SERVICE, major_version: 1, required: true };
}

/** Advertise only implemented operations. Missing operations fail explicitly. */
export function sessionDomainDescriptor(methods: readonly SessionDomainMethod[] = SESSION_DOMAIN_METHODS): ServiceDescriptor {
  return {
    name: SESSION_DOMAIN_SERVICE, version: "1.0.0",
    methods: methods.map(name => ({ name,
      input_schema: `${SESSION_DOMAIN_SERVICE}.${name}.input.v1`,
      output_schema: `${SESSION_DOMAIN_SERVICE}.${name}.output.v1`,
      ...(name === "Journal.Close" ? { cleanup: true } : {}),
    })),
  };
}

export interface SessionDomainContext {
  readonly caller: string;
  readonly executionId?: string;
  readonly callerScope?: ServiceInvokeParams["caller_scope"];
}
export type SessionDomainHandlers = {
  [K in SessionDomainMethod]?: (
    args: SessionDomainMethods[K]["args"], context: SessionDomainContext,
  ) => SessionDomainMethods[K]["values"] | Promise<SessionDomainMethods[K]["values"]>;
};

/** Process-provider adapter; no default storage or session behavior is supplied.
 * Keep resources in this runtime generation, authenticate each journal handle
 * against context.caller, and drain them in the provider's dispose handler.
 * AdmitThreadExecution receives the requested thread, domain limit, OS-active
 * IDs and the full probed ID set. Atomically reject new unprobed members with
 * session_admission_changed. The host alone owns OS locks and probes.
 */
export function sessionDomainProvider(handlers: SessionDomainHandlers) {
  const implemented = Object.keys(handlers) as SessionDomainMethod[];
  return {
    descriptor: sessionDomainDescriptor(implemented),
    async invoke(request: ServiceInvokeParams): Promise<{ values: readonly unknown[] }> {
      if (request.service !== SESSION_DOMAIN_SERVICE || !Object.hasOwn(handlers, request.method)) {
        throw Object.assign(new Error("Session domain operation is not implemented"), { code: "method_not_found" });
      }
      const method = request.method as SessionDomainMethod;
      const args = (request.params as { args?: unknown } | undefined)?.args;
      if (!Array.isArray(args) || args.length !== sessionDomainArity[method][0]) {
        throw Object.assign(new Error("Session domain argument count mismatch"), { code: "invalid_request" });
      }
      const handler = handlers[method] as ((args: readonly unknown[], context: SessionDomainContext) => readonly unknown[] | Promise<readonly unknown[]>) | undefined;
      if (!handler) throw Object.assign(new Error("Session domain operation is not implemented"), { code: "method_not_found" });
      const values = await handler(args, { caller: request.caller, executionId: request.execution_id, callerScope: request.caller_scope });
      if (!Array.isArray(values) || values.length !== sessionDomainArity[method][1]) {
        throw Object.assign(new Error("Session domain result count mismatch"), { code: "invalid_response" });
      }
      return { values };
    },
  };
}

/** Typed consumer of the selected binding. No default domain logic or fallback. */
export function sessionDomain(host: RuntimeHost) {
  return {
    async call<K extends SessionDomainMethod>(method: K, args: SessionDomainMethods[K]["args"]): Promise<SessionDomainMethods[K]["values"]> {
      const result = await host.call("host.service.call", { service: SESSION_DOMAIN_SERVICE, method, params: { args } }) as { values?: unknown };
      if (!Array.isArray(result?.values) || result.values.length !== sessionDomainArity[method][1]) {
        throw new Error(`Invalid ${SESSION_DOMAIN_SERVICE}.${method} result`);
      }
      return result.values as unknown as SessionDomainMethods[K]["values"];
    },
  };
}

// Code generated from the session domain operation inventory; DO NOT EDIT.
package session

import (
	"encoding/json"
	"github.com/blueberrycongee/wuu/internal/participant"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"time"
)

func (s *Store) RewriteHistoryRecordsAtBaseline(id string, records []HistoryRecord, baselineSeq int) error {
	err := s.invoke("RewriteHistoryRecordsAtBaseline", []any{id, records, baselineSeq}, []any{})
	return err
}
func (s *Store) RewriteHistoryRecordsForEdit(id string, records []HistoryRecord, fromSeq, baselineSeq int) error {
	err := s.invoke("RewriteHistoryRecordsForEdit", []any{id, records, fromSeq, baselineSeq}, []any{})
	return err
}
func (s *Store) StoreHistoryCheckpointAtBaseline(id, kind string, replacement []HistoryRecord, baselineSeq int) (HistoryCheckpoint, error) {
	var r0 HistoryCheckpoint
	err := s.invoke("StoreHistoryCheckpointAtBaseline", []any{id, kind, replacement, baselineSeq}, []any{&r0})
	return r0, err
}
func (s *Store) StoreContextWindow(id string, replacement []HistoryRecord, baselineSeq int, note CompactionNote) (HistoryCheckpoint, error) {
	var r0 HistoryCheckpoint
	err := s.invoke("StoreContextWindow", []any{id, replacement, baselineSeq, note}, []any{&r0})
	return r0, err
}
func (s *Store) RewriteHistoryRecordsForEngineEdit(id string, records []HistoryRecord, fromSeq, baselineSeq int, oldRef, newRef string) error {
	err := s.invoke("RewriteHistoryRecordsForEngineEdit", []any{id, records, fromSeq, baselineSeq, oldRef, newRef}, []any{})
	return err
}
func (s *Store) LoadCompactionNote(sessionID, providerKey string) (CompactionNote, bool, error) {
	var r0 CompactionNote
	var r1 bool
	err := s.invoke("LoadCompactionNote", []any{sessionID, providerKey}, []any{&r0, &r1})
	return r0, r1, err
}
func (s *Store) StoreCompactionNote(note CompactionNote) error {
	err := s.invoke("StoreCompactionNote", []any{note}, []any{})
	return err
}
func (s *Store) CompareAndSwapCompactionNote(expected CompactionNote, expectedExists bool, replacement CompactionNote) (bool, error) {
	var r0 bool
	err := s.invoke("CompareAndSwapCompactionNote", []any{expected, expectedExists, replacement}, []any{&r0})
	return r0, err
}
func (s *Store) PutContextSeed(seed ContextSeed) (ContextSeed, error) {
	var r0 ContextSeed
	err := s.invoke("PutContextSeed", []any{seed}, []any{&r0})
	return r0, err
}
func (s *Store) LoadContextSeed(id string) (ContextSeed, bool, error) {
	var r0 ContextSeed
	var r1 bool
	err := s.invoke("LoadContextSeed", []any{id}, []any{&r0, &r1})
	return r0, r1, err
}
func (s *Store) PutSessionLaunch(record SessionLaunchRecord) (SessionLaunchRecord, error) {
	var r0 SessionLaunchRecord
	err := s.invoke("PutSessionLaunch", []any{record}, []any{&r0})
	return r0, err
}
func (s *Store) LoadSessionLaunch(requestID string) (SessionLaunchRecord, bool, error) {
	var r0 SessionLaunchRecord
	var r1 bool
	err := s.invoke("LoadSessionLaunch", []any{requestID}, []any{&r0, &r1})
	return r0, r1, err
}
func (s *Store) ReadControl(id string) (Control, bool, error) {
	var r0 Control
	var r1 bool
	err := s.invoke("ReadControl", []any{id}, []any{&r0, &r1})
	return r0, r1, err
}
func (s *Store) ListControls() (map[string]Control, error) {
	var r0 map[string]Control
	err := s.invoke("ListControls", []any{}, []any{&r0})
	return r0, err
}
func (s *Store) ChangeControl(id, manager, state string, expected int64) (Control, error) {
	var r0 Control
	err := s.invoke("ChangeControl", []any{id, manager, state, expected}, []any{&r0})
	return r0, err
}
func (s *Store) ValidateControl(expected Control) error {
	err := s.invoke("ValidateControl", []any{expected}, []any{})
	return err
}
func (s *Store) ValidateContinuationControl(baseline Control) error {
	err := s.invoke("ValidateContinuationControl", []any{baseline}, []any{})
	return err
}
func (s *Store) CreateInitialized(sess Session, records []HistoryRecord) (*Session, error) {
	var r0 *Session
	err := s.invoke("CreateInitialized", []any{sess, records}, []any{&r0})
	return r0, err
}
func (s *Store) CreateInitializedWithLaunch(sess Session, records []HistoryRecord, seed ContextSeed, launch SessionLaunchRecord) (*Session, error) {
	var r0 *Session
	err := s.invoke("CreateInitializedWithLaunch", []any{sess, records, seed, launch}, []any{&r0})
	return r0, err
}
func (s *Store) CreateInitializedWithInbox(sess Session, records []HistoryRecord, seed ContextSeed, launch SessionLaunchRecord, input InboxMessage) (*Session, error) {
	var r0 *Session
	err := s.invoke("CreateInitializedWithInbox", []any{sess, records, seed, launch, input}, []any{&r0})
	return r0, err
}
func (s *Store) Create(id ...string) (*Session, error) {
	var r0 *Session
	err := s.invoke("Create", []any{id}, []any{&r0})
	return r0, err
}
func (s *Store) CreateWithMetadata(id, cwd string) (*Session, error) {
	var r0 *Session
	err := s.invoke("CreateWithMetadata", []any{id, cwd}, []any{&r0})
	return r0, err
}
func (s *Store) CreateManagedWithMetadata(id, cwd string, managed ManagedMetadata) (*Session, error) {
	var r0 *Session
	err := s.invoke("CreateManagedWithMetadata", []any{id, cwd, managed}, []any{&r0})
	return r0, err
}
func (s *Store) CreateForkWithMetadata(id, cwd string, fork ForkMetadata) (*Session, error) {
	var r0 *Session
	err := s.invoke("CreateForkWithMetadata", []any{id, cwd, fork}, []any{&r0})
	return r0, err
}
func (s *Store) CreateManagedForkWithMetadata(id, cwd string, fork ForkMetadata, managed ManagedMetadata) (*Session, error) {
	var r0 *Session
	err := s.invoke("CreateManagedForkWithMetadata", []any{id, cwd, fork, managed}, []any{&r0})
	return r0, err
}
func (s *Store) CreateWithWorktree(id, cwd string, fork ForkMetadata, worktree WorktreeInfo) (*Session, error) {
	var r0 *Session
	err := s.invoke("CreateWithWorktree", []any{id, cwd, fork, worktree}, []any{&r0})
	return r0, err
}
func (s *Store) CreateManagedWithWorktree(id, cwd string, fork ForkMetadata, worktree WorktreeInfo, managed ManagedMetadata) (*Session, error) {
	var r0 *Session
	err := s.invoke("CreateManagedWithWorktree", []any{id, cwd, fork, worktree, managed}, []any{&r0})
	return r0, err
}
func (s *Store) Delete(id string) (Session, error) {
	var r0 Session
	err := s.invoke("Delete", []any{id}, []any{&r0})
	return r0, err
}
func (s *Store) DeleteArchived(id string) (Session, error) {
	var r0 Session
	err := s.invoke("DeleteArchived", []any{id}, []any{&r0})
	return r0, err
}
func (s *Store) LoadDriverBinding(sessionID string) (pluginprotocol.DriverBinding, bool, error) {
	var r0 pluginprotocol.DriverBinding
	var r1 bool
	err := s.invoke("LoadDriverBinding", []any{sessionID}, []any{&r0, &r1})
	return r0, r1, err
}
func (s *Store) BindDriver(sessionID string, binding pluginprotocol.DriverBinding) error {
	err := s.invoke("BindDriver", []any{sessionID, binding}, []any{})
	return err
}
func (s *Store) LoadDriverCheckpoint(sessionID string) (DriverCheckpointRecord, bool, error) {
	var r0 DriverCheckpointRecord
	var r1 bool
	err := s.invoke("LoadDriverCheckpoint", []any{sessionID}, []any{&r0, &r1})
	return r0, r1, err
}
func (s *Store) SaveDriverCheckpoint(sessionID string, record DriverCheckpointRecord) error {
	err := s.invoke("SaveDriverCheckpoint", []any{sessionID, record}, []any{})
	return err
}
func (s *Store) SetHistoryEngineTurnID(id string, seq int, ref, turnID string, pending bool) error {
	err := s.invoke("SetHistoryEngineTurnID", []any{id, seq, ref, turnID, pending}, []any{})
	return err
}
func (s *Store) SaveModelInputReceipt(sessionID string, record ModelInputReceiptRecord) error {
	err := s.invoke("SaveModelInputReceipt", []any{sessionID, record}, []any{})
	return err
}
func (s *Store) LoadModelInputReceipts(sessionID string) ([]ModelInputReceiptRecord, error) {
	var r0 []ModelInputReceiptRecord
	err := s.invoke("LoadModelInputReceipts", []any{sessionID}, []any{&r0})
	return r0, err
}

func (s *Store) ReplaceHeldUserWork(sessionID string, entries []HeldUserWork) error {
	err := s.invoke("ReplaceHeldUserWork", []any{sessionID, entries}, []any{})
	return err
}
func (s *Store) LoadHeldUserWork(sessionID string) ([]HeldUserWork, error) {
	var r0 []HeldUserWork
	err := s.invoke("LoadHeldUserWork", []any{sessionID}, []any{&r0})
	return r0, err
}
func (s *Store) DeleteHeldUserWork(sessionID, id string) (bool, error) {
	var r0 bool
	err := s.invoke("DeleteHeldUserWork", []any{sessionID, id}, []any{&r0})
	return r0, err
}
func (s *Store) AcceptHeldUserWork(sessionID string, entry HeldUserWork) (bool, error) {
	var r0 bool
	err := s.invoke("AcceptHeldUserWork", []any{sessionID, entry}, []any{&r0})
	return r0, err
}
func (s *Store) UpdateHeldUserWork(sessionID string, entry, expected HeldUserWork) (bool, error) {
	var r0 bool
	err := s.invoke("UpdateHeldUserWork", []any{sessionID, entry, expected}, []any{&r0})
	return r0, err
}
func (s *Store) CancelHeldUserWork(sessionID, id, origin string) (bool, error) {
	var r0 bool
	err := s.invoke("CancelHeldUserWork", []any{sessionID, id, origin}, []any{&r0})
	return r0, err
}
func (s *Store) HasHeldUserWorkReceipt(sessionID, id string) (bool, error) {
	var r0 bool
	err := s.invoke("HasHeldUserWorkReceipt", []any{sessionID, id}, []any{&r0})
	return r0, err
}
func (s *Store) ClaimHeldUserWork(sessionID string, entry HeldUserWork) error {
	err := s.invoke("ClaimHeldUserWork", []any{sessionID, entry}, []any{})
	return err
}
func (s *Store) ReleaseHeldUserWork(sessionID, id string) error {
	err := s.invoke("ReleaseHeldUserWork", []any{sessionID, id}, []any{})
	return err
}
func (s *Store) AppendHistoryRecord(id string, record HistoryRecord) error {
	err := s.invoke("AppendHistoryRecord", []any{id, record}, []any{})
	return err
}
func (s *Store) AppendHistoryRecordReturningSeq(id string, record HistoryRecord) (int, error) {
	var r0 int
	err := s.invoke("AppendHistoryRecordReturningSeq", []any{id, record}, []any{&r0})
	return r0, err
}
func (s *Store) AppendControlledHistoryRecord(id string, record HistoryRecord, control *Control) (int, error) {
	var r0 int
	err := s.invoke("AppendControlledHistoryRecord", []any{id, record, control}, []any{&r0})
	return r0, err
}
func (s *Store) AppendContinuationHistoryRecord(id string, record HistoryRecord, control *Control, baseline Control) (int, error) {
	var r0 int
	err := s.invoke("AppendContinuationHistoryRecord", []any{id, record, control, baseline}, []any{&r0})
	return r0, err
}
func (s *Store) AppendAcceptedUserHistoryRecord(id string, record HistoryRecord, accepted HeldUserWork) (int, error) {
	var r0 int
	err := s.invoke("AppendAcceptedUserHistoryRecord", []any{id, record, accepted}, []any{&r0})
	return r0, err
}
func (s *Store) AppendHistoryRecords(id string, records []HistoryRecord) error {
	err := s.invoke("AppendHistoryRecords", []any{id, records}, []any{})
	return err
}
func (s *Store) AppendHistoryRecordsReturningRange(id string, records []HistoryRecord) (int, int, error) {
	var r0 int
	var r1 int
	err := s.invoke("AppendHistoryRecordsReturningRange", []any{id, records}, []any{&r0, &r1})
	return r0, r1, err
}
func (s *Store) RewriteHistoryRecords(id string, records []HistoryRecord) error {
	err := s.invoke("RewriteHistoryRecords", []any{id, records}, []any{})
	return err
}
func (s *Store) LoadHistoryRecords(id string, includeMeta bool) ([]HistoryRecord, error) {
	var r0 []HistoryRecord
	err := s.invoke("LoadHistoryRecords", []any{id, includeMeta}, []any{&r0})
	return r0, err
}
func (s *Store) LoadActiveHistoryRecords(id string, includeMeta bool) ([]HistoryRecord, error) {
	var r0 []HistoryRecord
	err := s.invoke("LoadActiveHistoryRecords", []any{id, includeMeta}, []any{&r0})
	return r0, err
}
func (s *Store) LatestHistoryCheckpoint(id string) (HistoryCheckpoint, bool, error) {
	var r0 HistoryCheckpoint
	var r1 bool
	err := s.invoke("LatestHistoryCheckpoint", []any{id}, []any{&r0, &r1})
	return r0, r1, err
}
func (s *Store) LoadProviderHistorySnapshot(id string) (ProviderHistorySnapshot, error) {
	var r0 ProviderHistorySnapshot
	err := s.invoke("LoadProviderHistorySnapshot", []any{id}, []any{&r0})
	return r0, err
}
func (s *Store) ReadHistoryPage(id string, startSeq, limit int) (HistoryPage, error) {
	var r0 HistoryPage
	err := s.invoke("ReadHistoryPage", []any{id, startSeq, limit}, []any{&r0})
	return r0, err
}
func (s *Store) ReadHistoryQuery(query HistoryReadQuery) (HistoryPage, error) {
	var r0 HistoryPage
	err := s.invoke("ReadHistoryQuery", []any{query}, []any{&r0})
	return r0, err
}
func (s *Store) SearchHistoryPage(id, query string, beforeSeq, limit int) (HistoryPage, error) {
	var r0 HistoryPage
	err := s.invoke("SearchHistoryPage", []any{id, query, beforeSeq, limit}, []any{&r0})
	return r0, err
}
func (s *Store) SearchHistoryQuery(query HistorySearchQuery) (HistoryPage, error) {
	var r0 HistoryPage
	err := s.invoke("SearchHistoryQuery", []any{query}, []any{&r0})
	return r0, err
}
func (s *Store) EnqueueInbox(message InboxMessage) error {
	err := s.invoke("EnqueueInbox", []any{message}, []any{})
	return err
}
func (s *Store) SettleInbox(clientID, sessionID string) error {
	err := s.invoke("SettleInbox", []any{clientID, sessionID}, []any{})
	return err
}
func (s *Store) PendingInbox(sessionID string) ([]InboxMessage, error) {
	var r0 []InboxMessage
	err := s.invoke("PendingInbox", []any{sessionID}, []any{&r0})
	return r0, err
}
func (s *Store) InboxTargets() ([]string, error) {
	var r0 []string
	err := s.invoke("InboxTargets", []any{}, []any{&r0})
	return r0, err
}
func (s *Store) InboxHas(clientID string) (bool, error) {
	var r0 bool
	err := s.invoke("InboxHas", []any{clientID}, []any{&r0})
	return r0, err
}
func (s *Store) ValidateInboxControls(clientID string) error {
	err := s.invoke("ValidateInboxControls", []any{clientID}, []any{})
	return err
}
func (s *Store) RebuildSessionIndexes() error {
	err := s.invoke("RebuildSessionIndexes", []any{}, []any{})
	return err
}
func (s *Store) ListPage(query SessionListQuery) (SessionListPage, error) {
	var r0 SessionListPage
	err := s.invoke("ListPage", []any{query}, []any{&r0})
	return r0, err
}
func (s *Store) MaintainModelInputReceipts(now time.Time) (ModelInputReceiptMaintenanceResult, error) {
	var r0 ModelInputReceiptMaintenanceResult
	err := s.invoke("MaintainModelInputReceipts", []any{now}, []any{&r0})
	return r0, err
}
func (s *Store) MaintainRedundantStorage() (RedundantStorageMaintenanceResult, error) {
	var r0 RedundantStorageMaintenanceResult
	err := s.invoke("MaintainRedundantStorage", []any{}, []any{&r0})
	return r0, err
}
func (s *Store) UpdateIndex(id string, entries int, summary string) error {
	err := s.invoke("UpdateIndex", []any{id, entries, summary}, []any{})
	return err
}
func (s *Store) UpdateGeneratedTitle(id string, title string) (Session, error) {
	var r0 Session
	err := s.invoke("UpdateGeneratedTitle", []any{id, title}, []any{&r0})
	return r0, err
}
func (s *Store) UpdateTitle(id string, title string) (Session, error) {
	var r0 Session
	err := s.invoke("UpdateTitle", []any{id, title}, []any{&r0})
	return r0, err
}
func (s *Store) UpdateWorkspaceBinding(id, cwd, worktreePath, worktreeBaseHEAD, worktreeBaseRepo string) (Session, error) {
	var r0 Session
	err := s.invoke("UpdateWorkspaceBinding", []any{id, cwd, worktreePath, worktreeBaseHEAD, worktreeBaseRepo}, []any{&r0})
	return r0, err
}
func (s *Store) UpdatePinned(id string, pinned bool) (Session, error) {
	var r0 Session
	err := s.invoke("UpdatePinned", []any{id, pinned}, []any{&r0})
	return r0, err
}
func (s *Store) SetWorkspaceID(id, workspaceID string) (Session, error) {
	var r0 Session
	err := s.invoke("SetWorkspaceID", []any{id, workspaceID}, []any{&r0})
	return r0, err
}
func (s *Store) SetProjectMembership(id, source, parentID, instructions string) (Session, error) {
	var r0 Session
	err := s.invoke("SetProjectMembership", []any{id, source, parentID, instructions}, []any{&r0})
	return r0, err
}
func (s *Store) SetSource(id, source string) (Session, error) {
	var r0 Session
	err := s.invoke("SetSource", []any{id, source}, []any{&r0})
	return r0, err
}
func (s *Store) SetRuntimeSelection(id string, selection RuntimeSelection) (Session, error) {
	var r0 Session
	err := s.invoke("SetRuntimeSelection", []any{id, selection}, []any{&r0})
	return r0, err
}
func (s *Store) SetEngine(id, engineID string) (Session, error) {
	var r0 Session
	err := s.invoke("SetEngine", []any{id, engineID}, []any{&r0})
	return r0, err
}
func (s *Store) SetEngineRef(id, ref string) (Session, error) {
	var r0 Session
	err := s.invoke("SetEngineRef", []any{id, ref}, []any{&r0})
	return r0, err
}
func (s *Store) SetModelSelection(id, provider, model, variant string) (Session, error) {
	var r0 Session
	err := s.invoke("SetModelSelection", []any{id, provider, model, variant}, []any{&r0})
	return r0, err
}
func (s *Store) UpdateArchived(id string, archived bool) (Session, error) {
	var r0 Session
	err := s.invoke("UpdateArchived", []any{id, archived}, []any{&r0})
	return r0, err
}
func (s *Store) BindWorktree(id string, worktree WorktreeInfo) (Session, error) {
	var r0 Session
	err := s.invoke("BindWorktree", []any{id, worktree}, []any{&r0})
	return r0, err
}
func (s *Store) WorktreeInfoForSession(id string) (WorktreeInfo, bool, error) {
	var r0 WorktreeInfo
	var r1 bool
	err := s.invoke("WorktreeInfoForSession", []any{id}, []any{&r0, &r1})
	return r0, r1, err
}
func (s *Store) ListOrganization() (SessionOrganization, error) {
	var r0 SessionOrganization
	err := s.invoke("ListOrganization", []any{}, []any{&r0})
	return r0, err
}
func (s *Store) CreateFolder(name string) (OrganizationGroup, error) {
	var r0 OrganizationGroup
	err := s.invoke("CreateFolder", []any{name}, []any{&r0})
	return r0, err
}
func (s *Store) RenameFolder(id, name string) (OrganizationGroup, error) {
	var r0 OrganizationGroup
	err := s.invoke("RenameFolder", []any{id, name}, []any{&r0})
	return r0, err
}
func (s *Store) DeleteFolder(id string) error {
	err := s.invoke("DeleteFolder", []any{id}, []any{})
	return err
}
func (s *Store) ReorderFolders(ids []string) error {
	err := s.invoke("ReorderFolders", []any{ids}, []any{})
	return err
}
func (s *Store) UpdateOrganization(id string, folderID *string) (Session, error) {
	var r0 Session
	err := s.invoke("UpdateOrganization", []any{id, folderID}, []any{&r0})
	return r0, err
}
func (s *Store) UpsertParticipant(person participant.Participant) error {
	err := s.invoke("UpsertParticipant", []any{person}, []any{})
	return err
}
func (s *Store) UpsertParticipantRun(run ParticipantRun) error {
	err := s.invoke("UpsertParticipantRun", []any{run}, []any{})
	return err
}
func (s *Store) CompleteParticipantRun(participantID, agentID, outcome, summary string) error {
	err := s.invoke("CompleteParticipantRun", []any{participantID, agentID, outcome, summary}, []any{})
	return err
}
func (s *Store) ListParticipantRuns(participantID string, limit int) ([]ParticipantRun, error) {
	var r0 []ParticipantRun
	err := s.invoke("ListParticipantRuns", []any{participantID, limit}, []any{&r0})
	return r0, err
}
func (s *Store) GetParticipant(id string) (participant.Participant, error) {
	var r0 participant.Participant
	err := s.invoke("GetParticipant", []any{id}, []any{&r0})
	return r0, err
}
func (s *Store) ListParticipants(kind participant.Kind) ([]participant.Participant, error) {
	var r0 []participant.Participant
	err := s.invoke("ListParticipants", []any{kind}, []any{&r0})
	return r0, err
}
func (s *Store) CountParticipantsByKind(kind participant.Kind) (int, error) {
	var r0 int
	err := s.invoke("CountParticipantsByKind", []any{kind}, []any{&r0})
	return r0, err
}
func (s *Store) RetireParticipant(id string) error {
	err := s.invoke("RetireParticipant", []any{id}, []any{})
	return err
}
func (s *Store) FindRetiredParticipantByName(kind participant.Kind, name string) (participant.Participant, bool, error) {
	var r0 participant.Participant
	var r1 bool
	err := s.invoke("FindRetiredParticipantByName", []any{kind, name}, []any{&r0, &r1})
	return r0, r1, err
}
func (s *Store) PutPluginTurnLifecycleOutbox(pluginID, requestID string, payload json.RawMessage) error {
	err := s.invoke("PutPluginTurnLifecycleOutbox", []any{pluginID, requestID, payload}, []any{})
	return err
}
func (s *Store) DeletePluginTurnLifecycleOutbox(pluginID, requestID string) error {
	err := s.invoke("DeletePluginTurnLifecycleOutbox", []any{pluginID, requestID}, []any{})
	return err
}
func (s *Store) DeletePluginTurnLifecycleOutboxForPlugin(pluginID string) error {
	err := s.invoke("DeletePluginTurnLifecycleOutboxForPlugin", []any{pluginID}, []any{})
	return err
}
func (s *Store) ListPluginTurnLifecycleOutbox() ([]PluginTurnLifecycleOutboxEntry, error) {
	var r0 []PluginTurnLifecycleOutboxEntry
	err := s.invoke("ListPluginTurnLifecycleOutbox", []any{}, []any{&r0})
	return r0, err
}
func (s *Store) FindPluginTurnLifecycle(pluginID, requestID, sessionID, turnID string) (PluginTurnLifecycleEntry, bool, error) {
	var r0 PluginTurnLifecycleEntry
	var r1 bool
	err := s.invoke("FindPluginTurnLifecycle", []any{pluginID, requestID, sessionID, turnID}, []any{&r0, &r1})
	return r0, r1, err
}
func (s *Store) WritePluginGenerationSnapshot(sessionID string, snapshot PluginGenerationSnapshot) error {
	err := s.invoke("WritePluginGenerationSnapshot", []any{sessionID, snapshot}, []any{})
	return err
}
func (s *Store) ReadPluginGenerationSnapshot(sessionID string) (PluginGenerationSnapshot, bool, error) {
	var r0 PluginGenerationSnapshot
	var r1 bool
	err := s.invoke("ReadPluginGenerationSnapshot", []any{sessionID}, []any{&r0, &r1})
	return r0, r1, err
}
func (s *Store) LoadRequestContextCheckpoint(sessionID string) (json.RawMessage, error) {
	var r0 json.RawMessage
	err := s.invoke("LoadRequestContextCheckpoint", []any{sessionID}, []any{&r0})
	return r0, err
}
func (s *Store) SaveRequestContextCheckpoint(sessionID string, payload json.RawMessage) error {
	err := s.invoke("SaveRequestContextCheckpoint", []any{sessionID, payload}, []any{})
	return err
}
func (s *Store) ReadProjectWork(id string) (ProjectWork, error) {
	var r0 ProjectWork
	err := s.invoke("ReadProjectWork", []any{id}, []any{&r0})
	return r0, err
}
func (s *Store) ListProjectWork(projectID string) ([]ProjectWork, error) {
	var r0 []ProjectWork
	err := s.invoke("ListProjectWork", []any{projectID}, []any{&r0})
	return r0, err
}
func (s *Store) PendingProjectWork(projectID string) ([]ProjectWork, error) {
	var r0 []ProjectWork
	err := s.invoke("PendingProjectWork", []any{projectID}, []any{&r0})
	return r0, err
}
func (s *Store) ReconcileProjectWork(id string, version int) error {
	err := s.invoke("ReconcileProjectWork", []any{id, version}, []any{})
	return err
}
func (s *Store) ProjectWorkOperation(operationID string) (ProjectWork, bool, error) {
	var r0 ProjectWork
	var r1 bool
	err := s.invoke("ProjectWorkOperation", []any{operationID}, []any{&r0, &r1})
	return r0, r1, err
}
func (s *Store) SaveProjectWork(work ProjectWork, version int, operationID string) (ProjectWork, error) {
	var r0 ProjectWork
	err := s.invoke("SaveProjectWork", []any{work, version, operationID}, []any{&r0})
	return r0, err
}
func (s *Store) Find(id string) (Session, bool, error) {
	var r0 Session
	var r1 bool
	err := s.invoke("Find", []any{id}, []any{&r0, &r1})
	return r0, r1, err
}
func (s *Store) List(limit int) ([]Session, error) {
	var r0 []Session
	err := s.invoke("List", []any{limit}, []any{&r0})
	return r0, err
}
func (s *Store) ListForCWD(cwd, workspaceID string, limit int) ([]Session, error) {
	var r0 []Session
	err := s.invoke("ListForCWD", []any{cwd, workspaceID, limit}, []any{&r0})
	return r0, err
}
func (s *Store) FindPinnedArchived(ids []string) (map[string]PinnedArchived, error) {
	var r0 map[string]PinnedArchived
	err := s.invoke("FindPinnedArchived", []any{ids}, []any{&r0})
	return r0, err
}
func (s *Store) FindManagedByRequest(owner, requestID string) (Session, bool, error) {
	var r0 Session
	var r1 bool
	err := s.invoke("FindManagedByRequest", []any{owner, requestID}, []any{&r0, &r1})
	return r0, r1, err
}
func (s *Store) MostRecent() (string, error) {
	var r0 string
	err := s.invoke("MostRecent", []any{}, []any{&r0})
	return r0, err
}
func (s *Store) MostRecentForCWD(cwd, workspaceID string) (string, error) {
	var r0 string
	err := s.invoke("MostRecentForCWD", []any{cwd, workspaceID}, []any{&r0})
	return r0, err
}
func (s *Store) LatestSubscriptionActivity(keys []SubscriptionActivityKey) (map[SubscriptionActivityKey]SubscriptionActivity, error) {
	var r0 map[SubscriptionActivityKey]SubscriptionActivity
	err := s.invoke("LatestSubscriptionActivity", []any{keys}, []any{&r0})
	return r0, err
}
func (s *Store) ListTokenUsage() ([]TokenUsageRow, error) {
	var r0 []TokenUsageRow
	err := s.invoke("ListTokenUsage", []any{}, []any{&r0})
	return r0, err
}

var domainMethods = []string{"AdmitThreadExecution", "RequestThreadExecutionResetRecord", "ThreadExecutionResetRequested", "RewriteHistoryRecordsAtBaseline", "RewriteHistoryRecordsForEdit", "StoreHistoryCheckpointAtBaseline", "StoreContextWindow", "RewriteHistoryRecordsForEngineEdit", "LoadCompactionNote", "StoreCompactionNote", "CompareAndSwapCompactionNote", "PutContextSeed", "LoadContextSeed", "PutSessionLaunch", "LoadSessionLaunch", "ReadControl", "ListControls", "ChangeControl", "ValidateControl", "ValidateContinuationControl", "CreateInitialized", "CreateInitializedWithLaunch", "CreateInitializedWithInbox", "Create", "CreateWithMetadata", "CreateManagedWithMetadata", "CreateForkWithMetadata", "CreateManagedForkWithMetadata", "CreateWithWorktree", "CreateManagedWithWorktree", "Delete", "DeleteArchived", "LoadDriverBinding", "BindDriver", "LoadDriverCheckpoint", "SaveDriverCheckpoint", "SetHistoryEngineTurnID", "SaveModelInputReceipt", "LoadModelInputReceipts", "ReplaceHeldUserWork", "LoadHeldUserWork", "DeleteHeldUserWork", "AcceptHeldUserWork", "UpdateHeldUserWork", "CancelHeldUserWork", "HasHeldUserWorkReceipt", "ClaimHeldUserWork", "ReleaseHeldUserWork", "AppendHistoryRecord", "AppendHistoryRecordReturningSeq", "AppendControlledHistoryRecord", "AppendContinuationHistoryRecord", "AppendAcceptedUserHistoryRecord", "AppendHistoryRecords", "AppendHistoryRecordsReturningRange", "RewriteHistoryRecords", "LoadHistoryRecords", "LoadActiveHistoryRecords", "LatestHistoryCheckpoint", "LoadProviderHistorySnapshot", "ReadHistoryPage", "ReadHistoryQuery", "SearchHistoryPage", "SearchHistoryQuery", "EnqueueInbox", "SettleInbox", "PendingInbox", "InboxTargets", "InboxHas", "ValidateInboxControls", "NewInferenceJournalRuntime", "RebuildSessionIndexes", "ListPage", "MaintainModelInputReceipts", "MaintainRedundantStorage", "UpdateIndex", "UpdateGeneratedTitle", "UpdateTitle", "UpdateWorkspaceBinding", "UpdatePinned", "SetWorkspaceID", "SetProjectMembership", "SetSource", "SetRuntimeSelection", "SetEngine", "SetEngineRef", "SetModelSelection", "UpdateArchived", "BindWorktree", "WorktreeInfoForSession", "ListOrganization", "CreateFolder", "RenameFolder", "DeleteFolder", "ReorderFolders", "UpdateOrganization", "UpsertParticipant", "UpsertParticipantRun", "CompleteParticipantRun", "ListParticipantRuns", "GetParticipant", "ListParticipants", "CountParticipantsByKind", "RetireParticipant", "FindRetiredParticipantByName", "PutPluginTurnLifecycleOutbox", "DeletePluginTurnLifecycleOutbox", "DeletePluginTurnLifecycleOutboxForPlugin", "ListPluginTurnLifecycleOutbox", "FindPluginTurnLifecycle", "WritePluginGenerationSnapshot", "ReadPluginGenerationSnapshot", "LoadRequestContextCheckpoint", "SaveRequestContextCheckpoint", "ReadProjectWork", "ListProjectWork", "PendingProjectWork", "ReconcileProjectWork", "ProjectWorkOperation", "SaveProjectWork", "Find", "List", "ListForCWD", "FindPinnedArchived", "FindManagedByRequest", "MostRecent", "MostRecentForCWD", "LatestSubscriptionActivity", "ListTokenUsage"}

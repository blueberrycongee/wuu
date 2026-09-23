package channels

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

func (s *Service) SetReminder(ctx context.Context, params ReminderSetParams) (Reminder, error) {
	if _, err := s.AuthenticateAgent(ctx, params.AgentID, params.Token); err != nil {
		return Reminder{}, err
	}
	return s.setReminder(ctx, params, s.now())
}

func (s *Service) SetReminderAfter(ctx context.Context, params ReminderSetParams, delay time.Duration) (Reminder, error) {
	if _, err := s.AuthenticateAgent(ctx, params.AgentID, params.Token); err != nil {
		return Reminder{}, err
	}
	if delay < MinReminderDur {
		return Reminder{}, fmt.Errorf("reminder delay must be at least %s", MinReminderDur)
	}
	now := s.now()
	params.FireAt = now.Add(delay)
	return s.setReminder(ctx, params, now)
}

func (s *Service) setReminder(ctx context.Context, params ReminderSetParams, now time.Time) (Reminder, error) {
	params.AgentID = strings.TrimSpace(params.AgentID)
	params.Note = strings.TrimSpace(params.Note)
	params.RoomID = strings.TrimSpace(params.RoomID)
	params.ThreadID = strings.TrimSpace(params.ThreadID)
	if params.Note == "" {
		return Reminder{}, errors.New("reminder note is required")
	}
	if params.FireAt.Before(now.Add(MinReminderDur)) {
		return Reminder{}, fmt.Errorf("reminder fire time must be at least %s in the future", MinReminderDur)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Reminder{}, fmt.Errorf("begin reminder set: %w", err)
	}
	defer tx.Rollback()
	if err := requireActiveNamedAgentTx(ctx, tx, params.AgentID); err != nil {
		return Reminder{}, err
	}
	if params.RoomID != "" {
		if err := s.requireRoomAgentMemberTx(ctx, tx, params.RoomID, params.AgentID); err != nil {
			return Reminder{}, err
		}
	}
	if params.ThreadID != "" {
		message, err := loadMessageTx(ctx, tx, params.ThreadID)
		if err != nil {
			return Reminder{}, err
		}
		if message.RoomID != params.RoomID {
			return Reminder{}, errors.New("reminder thread does not belong to room")
		}
		if message.ThreadID != "" {
			return Reminder{}, errors.New("reminder thread target must be a root message")
		}
	}
	id, err := randomID("reminder", 12)
	if err != nil {
		return Reminder{}, err
	}
	reminder := Reminder{
		ID:        id,
		AgentID:   params.AgentID,
		FireAt:    params.FireAt,
		Note:      params.Note,
		RoomID:    params.RoomID,
		ThreadID:  params.ThreadID,
		State:     ReminderPending,
		CreatedAt: fromMillis(toMillis(now)),
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO reminders (id, agent_id, fire_at, note, room_id, thread_id, state, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		reminder.ID, reminder.AgentID, toMillis(reminder.FireAt), reminder.Note,
		nullableString(reminder.RoomID), nullableString(reminder.ThreadID), string(reminder.State), toMillis(reminder.CreatedAt)); err != nil {
		return Reminder{}, fmt.Errorf("insert reminder: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Reminder{}, fmt.Errorf("commit reminder set: %w", err)
	}
	return reminder, nil
}

func (s *Service) ListReminders(ctx context.Context, params ReminderListParams) ([]Reminder, error) {
	if _, err := s.AuthenticateAgent(ctx, params.AgentID, params.Token); err != nil {
		return nil, err
	}
	query := `
		SELECT id, agent_id, fire_at, note, room_id, thread_id, state, created_at
		FROM reminders WHERE agent_id = ?`
	args := []any{params.AgentID}
	if params.State != "" {
		query += ` AND state = ?`
		args = append(args, string(params.State))
	}
	query += ` ORDER BY fire_at, id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list reminders: %w", err)
	}
	defer rows.Close()
	reminders := make([]Reminder, 0)
	for rows.Next() {
		reminder, err := scanReminder(rows)
		if err != nil {
			return nil, err
		}
		reminders = append(reminders, reminder)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list reminders: %w", err)
	}
	return reminders, nil
}

func (s *Service) CancelReminder(ctx context.Context, params ReminderCancelParams) (Reminder, error) {
	if _, err := s.AuthenticateAgent(ctx, params.AgentID, params.Token); err != nil {
		return Reminder{}, err
	}
	params.ReminderID = strings.TrimSpace(params.ReminderID)
	if params.ReminderID == "" {
		return Reminder{}, errors.New("reminder id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Reminder{}, fmt.Errorf("begin reminder cancel: %w", err)
	}
	defer tx.Rollback()
	reminder, err := loadReminderTx(ctx, tx, params.ReminderID)
	if err != nil {
		return Reminder{}, err
	}
	if reminder.AgentID != params.AgentID {
		return Reminder{}, ErrUnauthorized
	}
	if reminder.State != ReminderPending {
		return Reminder{}, fmt.Errorf("%w: reminder %q is %s", ErrConflict, reminder.ID, reminder.State)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE reminders SET state = ? WHERE id = ?`,
		string(ReminderCancelled), reminder.ID); err != nil {
		return Reminder{}, fmt.Errorf("cancel reminder: %w", err)
	}
	reminder.State = ReminderCancelled
	if err := tx.Commit(); err != nil {
		return Reminder{}, fmt.Errorf("commit reminder cancel: %w", err)
	}
	return reminder, nil
}

func (s *Service) FireDueReminders(ctx context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin fire due reminders: %w", err)
	}
	defer tx.Rollback()
	now := toMillis(s.now())
	rows, err := tx.QueryContext(ctx, `
		UPDATE reminders SET state = 'fired'
		WHERE state = 'pending' AND fire_at <= ?
		RETURNING id, agent_id, fire_at, room_id, thread_id`,
		now)
	if err != nil {
		return nil, fmt.Errorf("claim due reminders: %w", err)
	}
	defer rows.Close()
	fired := make([]Reminder, 0)
	agentIDs := make(map[string]struct{})
	for rows.Next() {
		var reminder Reminder
		var roomID, threadID sql.NullString
		var fireAt int64
		if err := rows.Scan(&reminder.ID, &reminder.AgentID, &fireAt, &roomID, &threadID); err != nil {
			return nil, fmt.Errorf("scan fired reminder: %w", err)
		}
		reminder.FireAt = fromMillis(fireAt)
		reminder.State = ReminderFired
		reminder.RoomID = roomID.String
		reminder.ThreadID = threadID.String
		fired = append(fired, reminder)
		agentIDs[reminder.AgentID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate fired reminders: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close fired reminders: %w", err)
	}
	for _, reminder := range fired {
		roomID := reminder.RoomID
		if roomID == "" {
			roomID = ""
		}
		if err := insertReminderInboxTx(ctx, tx, reminder.AgentID, roomID, reminder.ID, now); err != nil {
			return nil, err
		}
	}
	wake := make([]string, 0, len(agentIDs))
	for agentID := range agentIDs {
		requested, err := requestWakeTx(ctx, tx, agentID, now)
		if err != nil {
			return nil, fmt.Errorf("request reminder wake: %w", err)
		}
		if requested {
			wake = append(wake, agentID)
		}
	}
	sort.Strings(wake)
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit fire due reminders: %w", err)
	}
	if s.wake != nil {
		for _, agentID := range wake {
			s.wake.Deliver(agentID)
		}
	}
	return wake, nil
}

func scanReminder(row scanner) (Reminder, error) {
	var reminder Reminder
	var roomID, threadID sql.NullString
	var fireAt, createdAt int64
	if err := row.Scan(
		&reminder.ID, &reminder.AgentID, &fireAt, &reminder.Note,
		&roomID, &threadID, &reminder.State, &createdAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Reminder{}, ErrNotFound
		}
		return Reminder{}, fmt.Errorf("scan reminder: %w", err)
	}
	reminder.FireAt = fromMillis(fireAt)
	reminder.CreatedAt = fromMillis(createdAt)
	reminder.RoomID = roomID.String
	reminder.ThreadID = threadID.String
	return reminder, nil
}

func loadReminderTx(ctx context.Context, tx *sql.Tx, id string) (Reminder, error) {
	reminder, err := scanReminder(tx.QueryRowContext(ctx, `
		SELECT id, agent_id, fire_at, note, room_id, thread_id, state, created_at
		FROM reminders WHERE id = ?`, id))
	if errors.Is(err, ErrNotFound) {
		return Reminder{}, fmt.Errorf("%w: reminder %q", ErrNotFound, id)
	}
	return reminder, err
}

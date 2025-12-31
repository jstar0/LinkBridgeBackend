package storage

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
)

func (s *Store) CreateCall(ctx context.Context, callerID, calleeID, mediaType, groupID string, nowMs int64) (CallRow, error) {
	if s == nil || s.db == nil {
		return CallRow{}, fmt.Errorf("db not initialized")
	}
	if callerID == "" || calleeID == "" || groupID == "" {
		return CallRow{}, fmt.Errorf("missing required fields")
	}
	if callerID == calleeID {
		return CallRow{}, ErrCannotChatSelf
	}

	callID := uuid.NewString()
	call := CallRow{
		ID:          callID,
		GroupID:     groupID,
		CallerID:    callerID,
		CalleeID:    calleeID,
		MediaType:   mediaType,
		Status:      CallStatusInviting,
		CreatedAtMs: nowMs,
		UpdatedAtMs: nowMs,
	}

	q := `INSERT INTO calls (id, group_id, caller_id, callee_id, media_type, status, created_at_ms, updated_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?);`
	if _, err := s.db.ExecContext(ctx, s.rebind(q),
		call.ID, call.GroupID, call.CallerID, call.CalleeID, call.MediaType, call.Status, call.CreatedAtMs, call.UpdatedAtMs,
	); err != nil {
		return CallRow{}, err
	}

	return call, nil
}

func (s *Store) GetCallByID(ctx context.Context, callID string) (CallRow, error) {
	if s == nil || s.db == nil {
		return CallRow{}, fmt.Errorf("db not initialized")
	}

	q := `SELECT id, group_id, caller_id, callee_id, media_type, status, created_at_ms, updated_at_ms
		FROM calls WHERE id = ?;`

	var call CallRow
	if err := s.db.QueryRowContext(ctx, s.rebind(q), callID).Scan(
		&call.ID,
		&call.GroupID,
		&call.CallerID,
		&call.CalleeID,
		&call.MediaType,
		&call.Status,
		&call.CreatedAtMs,
		&call.UpdatedAtMs,
	); err != nil {
		if err == sql.ErrNoRows {
			return CallRow{}, fmt.Errorf("%w: call", ErrNotFound)
		}
		return CallRow{}, err
	}

	return call, nil
}

func (s *Store) AcceptCall(ctx context.Context, callID, userID string, nowMs int64) (CallRow, error) {
	call, err := s.GetCallByID(ctx, callID)
	if err != nil {
		return CallRow{}, err
	}
	if call.CalleeID != userID {
		return CallRow{}, ErrAccessDenied
	}
	if call.Status != CallStatusInviting {
		return CallRow{}, ErrInvalidState
	}

	q := `UPDATE calls SET status = ?, updated_at_ms = ? WHERE id = ? AND status = ?;`
	res, err := s.db.ExecContext(ctx, s.rebind(q), CallStatusAccepted, nowMs, callID, CallStatusInviting)
	if err != nil {
		return CallRow{}, err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return CallRow{}, ErrInvalidState
	}

	call.Status = CallStatusAccepted
	call.UpdatedAtMs = nowMs
	return call, nil
}

func (s *Store) RejectCall(ctx context.Context, callID, userID string, nowMs int64) (CallRow, error) {
	call, err := s.GetCallByID(ctx, callID)
	if err != nil {
		return CallRow{}, err
	}
	if call.CalleeID != userID {
		return CallRow{}, ErrAccessDenied
	}
	if call.Status != CallStatusInviting {
		return CallRow{}, ErrInvalidState
	}

	q := `UPDATE calls SET status = ?, updated_at_ms = ? WHERE id = ? AND status = ?;`
	res, err := s.db.ExecContext(ctx, s.rebind(q), CallStatusRejected, nowMs, callID, CallStatusInviting)
	if err != nil {
		return CallRow{}, err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return CallRow{}, ErrInvalidState
	}

	call.Status = CallStatusRejected
	call.UpdatedAtMs = nowMs
	return call, nil
}

func (s *Store) CancelCall(ctx context.Context, callID, userID string, nowMs int64) (CallRow, error) {
	call, err := s.GetCallByID(ctx, callID)
	if err != nil {
		return CallRow{}, err
	}
	if call.CallerID != userID {
		return CallRow{}, ErrAccessDenied
	}
	if call.Status != CallStatusInviting {
		return CallRow{}, ErrInvalidState
	}

	q := `UPDATE calls SET status = ?, updated_at_ms = ? WHERE id = ? AND status = ?;`
	res, err := s.db.ExecContext(ctx, s.rebind(q), CallStatusCanceled, nowMs, callID, CallStatusInviting)
	if err != nil {
		return CallRow{}, err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return CallRow{}, ErrInvalidState
	}

	call.Status = CallStatusCanceled
	call.UpdatedAtMs = nowMs
	return call, nil
}

func (s *Store) EndCall(ctx context.Context, callID, userID string, nowMs int64) (CallRow, error) {
	call, err := s.GetCallByID(ctx, callID)
	if err != nil {
		return CallRow{}, err
	}
	if call.CallerID != userID && call.CalleeID != userID {
		return CallRow{}, ErrAccessDenied
	}
	if call.Status != CallStatusAccepted {
		return CallRow{}, ErrInvalidState
	}

	q := `UPDATE calls SET status = ?, updated_at_ms = ? WHERE id = ? AND status = ?;`
	res, err := s.db.ExecContext(ctx, s.rebind(q), CallStatusEnded, nowMs, callID, CallStatusAccepted)
	if err != nil {
		return CallRow{}, err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return CallRow{}, ErrInvalidState
	}

	call.Status = CallStatusEnded
	call.UpdatedAtMs = nowMs
	return call, nil
}

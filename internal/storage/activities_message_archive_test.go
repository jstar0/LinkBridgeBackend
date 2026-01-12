package storage

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestCreateMessage_ActivityEnded_AutoArchivesSession(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	store, err := Open(ctx, "sqlite::memory:", logger)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = store.Close() }()

	base := time.Date(2026, 1, 11, 10, 0, 0, 0, time.FixedZone("CST", 8*60*60)).UnixMilli()

	creator, err := store.CreateUser(ctx, "creator", "hash", "Creator", base)
	if err != nil {
		t.Fatalf("CreateUser(creator) error = %v", err)
	}
	member, err := store.CreateUser(ctx, "member", "hash", "Member", base)
	if err != nil {
		t.Fatalf("CreateUser(member) error = %v", err)
	}

	endAt := base + 60*1000
	activity, invite, err := store.CreateActivity(ctx, creator.ID, "Test Activity", nil, nil, &endAt, base)
	if err != nil {
		t.Fatalf("CreateActivity() error = %v", err)
	}

	_, session, _, err := store.ConsumeActivityInvite(ctx, member.ID, invite.Code, nil, nil, base+1000)
	if err != nil {
		t.Fatalf("ConsumeActivityInvite() error = %v", err)
	}
	if session.ID != activity.SessionID {
		t.Fatalf("session.ID = %q, want %q", session.ID, activity.SessionID)
	}

	afterEnd := endAt + 1
	text := "hi"
	if _, err := store.CreateMessage(ctx, session.ID, member.ID, MessageTypeText, &text, nil, afterEnd); err != ErrSessionArchived {
		t.Fatalf("CreateMessage(after end) error = %v, want ErrSessionArchived", err)
	}

	sess2, err := store.GetSessionByID(ctx, session.ID)
	if err != nil {
		t.Fatalf("GetSessionByID() error = %v", err)
	}
	if sess2.Status != SessionStatusArchived {
		t.Fatalf("session.Status = %q, want %q", sess2.Status, SessionStatusArchived)
	}
}

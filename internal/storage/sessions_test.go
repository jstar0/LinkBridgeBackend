package storage

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func TestSessionReactivateAndHide(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	store, err := Open(context.Background(), "sqlite::memory:", logger)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	nowMs := time.Now().UnixMilli()

	// Create test users
	user1, err := store.CreateUser(ctx, "user1", "hash1", "User 1", nowMs)
	if err != nil {
		t.Fatal(err)
	}
	user2, err := store.CreateUser(ctx, "user2", "hash2", "User 2", nowMs)
	if err != nil {
		t.Fatal(err)
	}

	// Create session
	session, _, err := store.CreateSession(ctx, user1.ID, user2.ID, nowMs)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Created session: %s", session.ID)

	// Archive session
	session, err = store.ArchiveSession(ctx, session.ID, user1.ID, nowMs)
	if err != nil {
		t.Fatal(err)
	}
	if session.Status != SessionStatusArchived {
		t.Errorf("Expected status %s, got %s", SessionStatusArchived, session.Status)
	}
	t.Logf("Archived session: %s (status: %s)", session.ID, session.Status)

	// Reactivate session
	session, err = store.ReactivateSession(ctx, session.ID, user1.ID, nowMs)
	if err != nil {
		t.Fatal(err)
	}
	if session.Status != SessionStatusActive {
		t.Errorf("Expected status %s, got %s", SessionStatusActive, session.Status)
	}
	if session.ReactivatedAtMs == nil {
		t.Error("Expected reactivatedAtMs to be set")
	}
	t.Logf("Reactivated session: %s (status: %s, reactivatedAt: %v)", session.ID, session.Status, session.ReactivatedAtMs)

	// Hide session for user1
	err = store.HideSession(ctx, session.ID, user1.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Hidden session for user1")

	// List sessions for user1 (should be empty)
	sessions, err := store.ListSessionsForUser(ctx, user1.ID, SessionStatusActive)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Errorf("Expected 0 sessions for user1, got %d", len(sessions))
	}
	t.Logf("User1 active sessions: %d", len(sessions))

	// List sessions for user2 (should have 1)
	sessions, err = store.ListSessionsForUser(ctx, user2.ID, SessionStatusActive)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Errorf("Expected 1 session for user2, got %d", len(sessions))
	}
	t.Logf("User2 active sessions: %d", len(sessions))

	t.Log("All tests passed!")
}

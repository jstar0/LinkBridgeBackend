package storage

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestAcceptSessionRequest_MapSetsSessionSourceAndDefaultGroup(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	store, err := Open(ctx, "sqlite::memory:", logger)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = store.Close() }()

	now := time.Date(2026, 1, 11, 10, 0, 0, 0, time.FixedZone("CST", 8*60*60)).UnixMilli()

	a, err := store.CreateUser(ctx, "a1", "hash", "A", now)
	if err != nil {
		t.Fatalf("CreateUser(a) error = %v", err)
	}
	b, err := store.CreateUser(ctx, "b1", "hash", "B", now)
	if err != nil {
		t.Fatalf("CreateUser(b) error = %v", err)
	}

	verify := "hi"
	req, _, err := store.CreateSessionRequest(ctx, a.ID, b.ID, SessionRequestSourceMap, &verify, now)
	if err != nil {
		t.Fatalf("CreateSessionRequest() error = %v", err)
	}

	_, session, err := store.AcceptSessionRequest(ctx, req.ID, b.ID, now+1000)
	if err != nil {
		t.Fatalf("AcceptSessionRequest() error = %v", err)
	}
	if session == nil {
		t.Fatalf("expected session to be created")
	}
	if session.Source != SessionSourceMap {
		t.Fatalf("session.Source = %q, want %q", session.Source, SessionSourceMap)
	}

	// Both sides should get a default "地图" group assignment (only-if-missing behavior).
	metaA, err := store.GetSessionUserMeta(ctx, session.ID, a.ID)
	if err != nil {
		t.Fatalf("GetSessionUserMeta(a) error = %v", err)
	}
	if metaA.GroupName == nil || *metaA.GroupName != "地图" {
		t.Fatalf("metaA.GroupName = %v, want %q", metaA.GroupName, "地图")
	}

	metaB, err := store.GetSessionUserMeta(ctx, session.ID, b.ID)
	if err != nil {
		t.Fatalf("GetSessionUserMeta(b) error = %v", err)
	}
	if metaB.GroupName == nil || *metaB.GroupName != "地图" {
		t.Fatalf("metaB.GroupName = %v, want %q", metaB.GroupName, "地图")
	}
}

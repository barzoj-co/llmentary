package lab

import (
	"context"
	"testing"
	"time"
)

func TestWaitQuietReturnsForEmptySession(t *testing.T) {
	store := NewMemoryStore()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	session, err := store.WaitQuiet(ctx, "empty", time.Millisecond, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if session.ID != "empty" {
		t.Fatalf("session id = %q, want empty", session.ID)
	}
	if len(session.Calls) != 0 {
		t.Fatalf("calls = %d, want 0", len(session.Calls))
	}
}

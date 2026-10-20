// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLookupDoesNotCreateSession(t *testing.T) {
	store := NewMemoryStore()
	if _, ok := store.Lookup("empty"); ok {
		t.Fatal("lookup of unknown session created it")
	}
	if got := store.List(""); len(got) != 0 {
		t.Fatalf("list after lookup = %+v", got)
	}
}

func TestWaitQuietUnknownSessionTimesOut(t *testing.T) {
	store := NewMemoryStore()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	session, err := store.WaitQuiet(ctx, "empty", time.Millisecond, 40*time.Millisecond, 0)
	if !errors.Is(err, ErrWaitTimeout) {
		t.Fatalf("err=%v want wait_timeout", err)
	}
	if session.ID != "empty" || len(session.Calls) != 0 {
		t.Fatalf("session=%+v", session)
	}
	if _, ok := store.Lookup("empty"); ok {
		t.Fatal("wait created a phantom session")
	}
}

func TestWaitQuietMinCalls(t *testing.T) {
	store := NewMemoryStore()
	go func() {
		time.Sleep(20 * time.Millisecond)
		_, id := store.Begin("s", Call{StartedAt: time.Now().UTC(), SessionSource: "header"})
		store.Finish("s", id, func(call *Call) { call.EndedAt = time.Now().UTC() })
		time.Sleep(20 * time.Millisecond)
		_, id = store.Begin("s", Call{StartedAt: time.Now().UTC(), SessionSource: "header"})
		store.Finish("s", id, func(call *Call) { call.EndedAt = time.Now().UTC() })
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	session, err := store.WaitQuiet(ctx, "s", 5*time.Millisecond, time.Second, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(session.Calls) != 2 {
		t.Fatalf("calls=%d", len(session.Calls))
	}
}

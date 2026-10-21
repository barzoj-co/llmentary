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

func TestWaitQuietAlreadyIdleReturnsImmediately(t *testing.T) {
	store := NewMemoryStore()
	_, id := store.Begin("s", Call{StartedAt: time.Now().UTC()})
	store.Finish("s", id, func(call *Call) { call.EndedAt = time.Now().UTC() })
	time.Sleep(2 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	if _, err := store.WaitQuiet(ctx, "s", 500*time.Millisecond, time.Second, 0); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed >= 50*time.Millisecond {
		t.Fatalf("idle wait took %s, want < 50ms", elapsed)
	}
}

func TestWaitQuietCountsFromLastChange(t *testing.T) {
	store := NewMemoryStore()
	_, id := store.Begin("s", Call{StartedAt: time.Now().UTC()})
	store.Finish("s", id, func(call *Call) { call.EndedAt = time.Now().UTC() })
	time.Sleep(100 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	if _, err := store.WaitQuiet(ctx, "s", 500*time.Millisecond, 2*time.Second, 0); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	if elapsed < 200*time.Millisecond || elapsed > 500*time.Millisecond {
		t.Fatalf("elapsed %s, want ≈400ms ±100ms (200–500ms)", elapsed)
	}
}

func TestWaitQuietActiveCallThenQuiet(t *testing.T) {
	store := NewMemoryStore()
	started := make(chan struct{})
	go func() {
		_, id := store.Begin("s", Call{StartedAt: time.Now().UTC()})
		close(started)
		time.Sleep(80 * time.Millisecond)
		store.Finish("s", id, func(call *Call) { call.EndedAt = time.Now().UTC() })
	}()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	if _, err := store.WaitQuiet(ctx, "s", 100*time.Millisecond, 2*time.Second, 1); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	if elapsed < 120*time.Millisecond {
		t.Fatalf("returned in %s; active call should keep wait open then quiet", elapsed)
	}
}

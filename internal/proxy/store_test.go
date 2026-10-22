// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/barzoj-co/llmentary/internal/specvalidate"
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

func TestSpecCountersRequestOnlyNotClean(t *testing.T) {
	got := specCounters([]Call{{
		Normalized: Normalized{
			Validation: &ValidationInfo{RequestChecked: true, ResponseChecked: false},
		},
	}})
	if got.Clean != 0 || got.RequestOnly != 1 || got.Checked != 0 || got.NotChecked != 0 {
		t.Fatalf("got=%+v", got)
	}
}

func TestSpecCountersJSONAliases(t *testing.T) {
	got := specCounters([]Call{
		{Normalized: Normalized{Validation: &ValidationInfo{RequestChecked: true, ResponseChecked: true}}},
		{Normalized: Normalized{Validation: &ValidationInfo{RequestChecked: true, ResponseChecked: false}}},
		{Normalized: Normalized{Validation: &ValidationInfo{RequestChecked: true, ResponseChecked: false}}},
	})
	if got.CallsInvariantBroken(3) {
		t.Fatalf("invariant: %+v", got)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]int
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	pairs := [][2]string{{"checked", "Checked"}, {"clean", "Clean"}, {"findings", "Findings"}, {"request_only", "RequestOnly"}, {"not_checked", "NotChecked"}}
	for _, p := range pairs {
		a, okA := m[p[0]]
		b, okB := m[p[1]]
		if !okA || !okB || a != b {
			t.Fatalf("%s=%d(%v) %s=%d(%v) body=%s", p[0], a, okA, p[1], b, okB, raw)
		}
	}
	if m["clean"] != 1 || m["request_only"] != 2 || m["findings"] != 0 || m["checked"] != 1 || m["not_checked"] != 0 {
		t.Fatalf("counters=%v", m)
	}
}

func (s SpecCounters) CallsInvariantBroken(calls int) bool {
	return calls != s.Checked+s.RequestOnly+s.NotChecked || s.Checked != s.Clean+s.Findings
}

func TestSpecLogValueFindingsBeatSkipped(t *testing.T) {
	reason := "body_too_large"
	findings := []specvalidate.Violation{{Keyword: "required", Count: 2}}
	got := specLogValue(Call{
		Normalized: Normalized{
			Validation:     &ValidationInfo{SkippedReason: &reason, RequestChecked: false, ResponseChecked: true},
			SpecViolations: &findings,
		},
	}, "warn")
	if got != "findings:2" {
		t.Fatalf("got=%q want findings:2", got)
	}
}

func TestSpecLogValueSkipsResponseShape(t *testing.T) {
	info := []specvalidate.Violation{
		{Keyword: specvalidate.KeywordResponseShape, Side: specvalidate.SideResponse, Severity: specvalidate.SeverityInfo, Count: 4},
		{Keyword: "required", Side: specvalidate.SideRequest, Count: 1},
	}
	got := specLogValue(Call{
		Normalized: Normalized{
			Validation:     &ValidationInfo{RequestChecked: true, ResponseChecked: true},
			SpecViolations: &info,
		},
	}, "warn")
	if got != "findings:1" {
		t.Fatalf("got=%q want findings:1", got)
	}
	onlyInfo := []specvalidate.Violation{{Keyword: specvalidate.KeywordResponseShape, Severity: specvalidate.SeverityInfo, Count: 9}}
	got = specLogValue(Call{
		Normalized: Normalized{
			Validation:     &ValidationInfo{RequestChecked: true, ResponseChecked: true},
			SpecViolations: &onlyInfo,
		},
	}, "warn")
	if got != "clean" {
		t.Fatalf("got=%q want clean", got)
	}
}

func TestWaitQuietUnknownSessionNotFound(t *testing.T) {
	store := NewMemoryStore()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	session, err := store.WaitQuiet(ctx, "empty", 200*time.Millisecond, 3*time.Second, 0)
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("err=%v want session_not_found", err)
	}
	if session.ID != "empty" || len(session.Calls) != 0 {
		t.Fatalf("session=%+v", session)
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("min_calls=0 wait took %s, want immediate", elapsed)
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

func TestWaitQuietDeleteResetsQuietWindow(t *testing.T) {
	store := NewMemoryStore()
	store.Begin("s", Call{StartedAt: time.Now().UTC()})
	time.Sleep(150 * time.Millisecond)
	go func() {
		time.Sleep(20 * time.Millisecond)
		store.Delete("s")
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	_, err := store.WaitQuiet(ctx, "s", 80*time.Millisecond, time.Second, 0)
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("err=%v want session_not_found after delete", err)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("min_calls=0 after delete took %s", elapsed)
	}
}

func TestFinishIgnoresStaleCallAfterReuse(t *testing.T) {
	store := NewMemoryStore()
	_, oldID := store.Begin("s", Call{StartedAt: time.Now().UTC()})
	store.Delete("s")
	_, _ = store.Begin("s", Call{StartedAt: time.Now().UTC()})
	store.Finish("s", oldID, func(call *Call) { call.EndedAt = time.Now().UTC() })

	session, ok := store.Lookup("s")
	if !ok {
		t.Fatal("reused session missing")
	}
	if len(session.Calls) != 0 {
		t.Fatalf("in-flight call must not be published: %+v", session.Calls)
	}
	summaries := store.List("")
	if len(summaries) != 1 || summaries[0].Active != 1 || summaries[0].Calls != 0 {
		t.Fatalf("summaries=%+v", summaries)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := store.WaitQuiet(ctx, "s", 40*time.Millisecond, 80*time.Millisecond, 0)
	if !errors.Is(err, ErrWaitTimeout) {
		t.Fatalf("err=%v want wait_timeout while reused call is still active", err)
	}
	if elapsed := time.Since(start); elapsed < 40*time.Millisecond {
		t.Fatalf("wait returned in %s; stale finish must not idle the new call", elapsed)
	}
}

func TestFinishUnknownSessionDoesNotCreate(t *testing.T) {
	store := NewMemoryStore()
	store.Finish("empty", "call_1", func(call *Call) { call.EndedAt = time.Now().UTC() })
	if _, ok := store.Lookup("empty"); ok {
		t.Fatal("finish created a phantom session")
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

func TestLookupHidesInFlightPlaceholder(t *testing.T) {
	store := NewMemoryStore()
	_, id := store.Begin("s", Call{StartedAt: time.Now().UTC(), HTTPStatus: 0})
	session, ok := store.Lookup("s")
	if !ok {
		t.Fatal("session missing after Begin")
	}
	if len(session.Calls) != 0 {
		t.Fatalf("GET-equivalent Lookup exposed in-flight placeholder: %+v", session.Calls)
	}
	summaries := store.List("")
	if len(summaries) != 1 || summaries[0].Active != 1 || summaries[0].Calls != 0 {
		t.Fatalf("list during in-flight=%+v", summaries)
	}

	store.Finish("s", id, func(call *Call) {
		call.EndedAt = time.Now().UTC()
		call.ClientAborted = true
		call.AbortPhase = AbortPhaseBeforeHeaders
		call.HTTPStatus = 0
		call.Normalized.FinishReason = "cancelled"
		call.Normalized.ClientAborted = true
	})
	session, ok = store.Lookup("s")
	if !ok || len(session.Calls) != 1 {
		t.Fatalf("settled session=%+v ok=%v", session, ok)
	}
	got := session.Calls[0]
	if got.ID != id || !got.ClientAborted || got.AbortPhase != AbortPhaseBeforeHeaders || got.Normalized.FinishReason != "cancelled" {
		t.Fatalf("settled call=%+v", got)
	}
}

func TestWaitMinCallsCountsOnlySettled(t *testing.T) {
	store := NewMemoryStore()
	_, _ = store.Begin("s", Call{StartedAt: time.Now().UTC()})
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := store.WaitQuiet(ctx, "s", 20*time.Millisecond, 80*time.Millisecond, 1)
	if !errors.Is(err, ErrWaitTimeout) {
		t.Fatalf("err=%v want wait_timeout while only an in-flight call exists", err)
	}
	if elapsed := time.Since(start); elapsed < 40*time.Millisecond {
		t.Fatalf("wait returned in %s; in-flight must not count as min_calls", elapsed)
	}
}

func TestConcurrentGetDuringFinishHook(t *testing.T) {
	store := NewMemoryStore()
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	store.SetBeforeFinish(func() {
		once.Do(func() { close(entered) })
		<-release
	})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})

	_, id := store.Begin("s", Call{StartedAt: time.Now().UTC()})

	waitDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, err := store.WaitQuiet(ctx, "s", 10*time.Millisecond, time.Second, 1)
		waitDone <- err
	}()

	finishDone := make(chan struct{})
	go func() {
		defer close(finishDone)
		store.Finish("s", id, func(call *Call) {
			call.EndedAt = time.Now().UTC()
			call.ClientAborted = true
			call.AbortPhase = AbortPhaseAfterHeaders
			call.HTTPStatus = 200
			call.Normalized.FinishReason = "cancelled"
		})
	}()

	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("Finish hook did not run")
	}

	for i := 0; i < 20; i++ {
		session, ok := store.Lookup("s")
		if !ok {
			t.Fatal("session missing during finalize")
		}
		if len(session.Calls) != 0 {
			t.Fatalf("Lookup during finalize exposed placeholder: %+v", session.Calls)
		}
		summaries := store.List("")
		if len(summaries) != 1 || summaries[0].Active != 1 || summaries[0].Calls != 0 {
			t.Fatalf("list during finalize=%+v", summaries)
		}
		select {
		case err := <-waitDone:
			t.Fatalf("wait returned during finalize: %v", err)
		default:
		}
		time.Sleep(2 * time.Millisecond)
	}

	close(release)
	<-finishDone
	if err := <-waitDone; err != nil {
		t.Fatalf("wait after publish: %v", err)
	}
	session, ok := store.Lookup("s")
	if !ok || len(session.Calls) != 1 {
		t.Fatalf("after publish session=%+v ok=%v", session, ok)
	}
	if !session.Calls[0].ClientAborted || session.Calls[0].AbortPhase != AbortPhaseAfterHeaders {
		t.Fatalf("settled=%+v", session.Calls[0])
	}
}

func TestConcurrentGetNeverSeesPlaceholder(t *testing.T) {
	for i := 0; i < 20; i++ {
		store := NewMemoryStore()
		started := make(chan struct{})
		var startOnce sync.Once
		seenEmpty := make(chan struct{})
		var seenOnce sync.Once
		done := make(chan struct{})
		errCh := make(chan error, 1)
		go func() {
			defer close(errCh)
			startOnce.Do(func() { close(started) })
			for {
				select {
				case <-done:
					return
				default:
				}
				session, ok := store.Lookup("s")
				if !ok {
					continue
				}
				if len(session.Calls) == 0 {
					seenOnce.Do(func() { close(seenEmpty) })
				}
				for _, call := range session.Calls {
					if !callSettled(call) {
						errCh <- errors.New("unsettled call visible")
						return
					}
					if call.HTTPStatus == 0 && !call.ClientAborted && call.AbortPhase == "" && call.Normalized.FinishReason == "" {
						errCh <- errors.New("abort placeholder visible")
						return
					}
				}
			}
		}()
		<-started
		_, id := store.Begin("s", Call{StartedAt: time.Now().UTC()})
		select {
		case <-seenEmpty:
		case <-time.After(time.Second):
			t.Fatal("reader never observed in-flight session")
		}
		store.Finish("s", id, func(call *Call) {
			call.EndedAt = time.Now().UTC()
			call.ClientAborted = true
			call.AbortPhase = AbortPhaseBeforeHeaders
			call.HTTPStatus = 0
			call.Normalized.FinishReason = "cancelled"
			call.Normalized.ClientAborted = true
		})
		close(done)
		if err := <-errCh; err != nil {
			t.Fatal(err)
		}
	}
}

func TestInFlightSnapshotOrderedAndHiddenFromLookup(t *testing.T) {
	store := NewMemoryStore()
	started := time.Now().UTC().Add(-2 * time.Second)
	_, id1 := store.Begin("s", Call{
		StartedAt: started,
		APIFamily: "openai.responses",
		Upstream:  Upstream{Method: httpMethodPost, URL: "http://127.0.0.1:9/v1/responses"},
	})
	_, id2 := store.Begin("s", Call{
		StartedAt: started,
		APIFamily: "openai.chat",
		Upstream:  Upstream{Method: httpMethodPost, URL: "http://127.0.0.1:9/v1/chat/completions"},
	})
	store.Begin("other", Call{
		StartedAt: started,
		APIFamily: "openai.chat",
		Upstream:  Upstream{Method: httpMethodPost, URL: "http://127.0.0.1:9/v1/chat/completions"},
	})
	store.Progress("s", id2, func(call *Call) {
		call.HTTPStatus = 200
		call.BytesReceived = 12
	})
	session, ok := store.Lookup("s")
	if !ok || len(session.Calls) != 0 {
		t.Fatalf("lookup leaked in-flight: %+v ok=%v", session, ok)
	}
	got := store.InFlight("s")
	if len(got) != 2 {
		t.Fatalf("in_flight=%+v", got)
	}
	if got[0].Seq != 1 || got[0].CallID != id1 || got[0].Phase != InFlightPhaseAwaitingHeaders || got[0].HTTPStatus != 0 {
		t.Fatalf("first=%+v", got[0])
	}
	if got[1].Seq != 2 || got[1].CallID != id2 || got[1].Phase != InFlightPhaseStreaming || got[1].HTTPStatus != 200 || got[1].BytesReceived != 12 {
		t.Fatalf("second=%+v", got[1])
	}
	if got[0].Path != "/v1/responses" || got[1].Path != "/v1/chat/completions" {
		t.Fatalf("paths %q %q", got[0].Path, got[1].Path)
	}
	if got[0].ElapsedMS < 1000 {
		t.Fatalf("elapsed_ms=%d", got[0].ElapsedMS)
	}
	other := store.InFlight("other")
	if len(other) != 1 || other[0].Seq != 1 {
		t.Fatalf("other=%+v", other)
	}
	store.Finish("s", id1, func(call *Call) {
		call.EndedAt = time.Now().UTC()
		call.HTTPStatus = 200
	})
	got = store.InFlight("s")
	if len(got) != 1 || got[0].CallID != id2 {
		t.Fatalf("after settle=%+v", got)
	}
}

func TestInFlightFromCallCorners(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	future := now.Add(5 * time.Second)
	fb := now.Add(-time.Millisecond)
	item := inFlightFromCall(Call{
		ID:          "call_x",
		Seq:         3,
		StartedAt:   future,
		FirstByteAt: &fb,
		HTTPStatus:  200,
		Upstream:    Upstream{Method: httpMethodPost, URL: "https://user:hunter2@api.example/v1/chat?api_key=secret"},
	}, now)
	if item.Phase != InFlightPhaseStreaming {
		t.Fatalf("phase=%s", item.Phase)
	}
	if item.ElapsedMS != 0 {
		t.Fatalf("future start elapsed=%d", item.ElapsedMS)
	}
	if item.Path != "/v1/chat" {
		t.Fatalf("path=%q", item.Path)
	}
	if strings.Contains(item.Upstream, "hunter2") || strings.Contains(item.Upstream, "user:") || strings.Contains(item.Upstream, "secret") || strings.Contains(item.Upstream, "?") {
		t.Fatalf("credentials leaked: %s", item.Upstream)
	}
	token := inFlightFromCall(Call{ID: "t", Upstream: Upstream{URL: "https://api.example/v1/chat?access_token=SECRET&keep=1"}}, now)
	if token.Upstream != "https://api.example/v1/chat" || token.Path != "/v1/chat" {
		t.Fatalf("access_token in-flight: %+v", token)
	}
	bad := inFlightFromCall(Call{ID: "c", Upstream: Upstream{URL: "://%"}}, now)
	if bad.Path != "" {
		t.Fatalf("invalid url path=%q", bad.Path)
	}
	awaiting := inFlightFromCall(Call{ID: "c", StartedAt: now.Add(-time.Second), Upstream: Upstream{URL: "http://127.0.0.1/v1/x"}}, now)
	if awaiting.Phase != InFlightPhaseAwaitingHeaders || awaiting.ElapsedMS < 1000 {
		t.Fatalf("awaiting=%+v", awaiting)
	}
}

func TestSessionAndInFlightPartitionsCalls(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	now := time.Now().UTC()
	_, id1 := store.Begin("s", Call{StartedAt: now, Upstream: Upstream{URL: "https://api.example/v1/a?access_token=SECRET"}})
	_, id2 := store.Begin("s", Call{StartedAt: now, Upstream: Upstream{URL: "https://api.example/v1/b?keep=1"}})
	store.Finish("s", id1, func(call *Call) {
		call.EndedAt = now
		call.HTTPStatus = 200
	})
	sess, inflight, ok := store.SessionAndInFlight("s")
	if !ok {
		t.Fatal("missing session")
	}
	if len(sess.Calls) != 1 || sess.Calls[0].ID != id1 {
		t.Fatalf("settled=%+v", sess.Calls)
	}
	if len(inflight) != 1 || inflight[0].CallID != id2 {
		t.Fatalf("inflight=%+v", inflight)
	}
	if strings.Contains(inflight[0].Upstream, "?") || strings.Contains(inflight[0].Upstream, "keep") {
		t.Fatalf("query leaked: %s", inflight[0].Upstream)
	}
	if _, _, ok := store.SessionAndInFlight("ghost"); ok {
		t.Fatal("ghost exists")
	}
	if got := store.InFlight("ghost"); got != nil {
		t.Fatalf("ghost inflight=%v", got)
	}
}

func TestSessionAndInFlightConcurrentFinish(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	_, id := store.Begin("s", Call{StartedAt: time.Now().UTC(), Upstream: Upstream{URL: "https://api.example/v1"}})
	var mu sync.Mutex
	var fails []string
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			sess, inflight, ok := store.SessionAndInFlight("s")
			if !ok {
				mu.Lock()
				fails = append(fails, "session disappeared")
				mu.Unlock()
				return
			}
			if len(sess.Calls)+len(inflight) != 1 {
				mu.Lock()
				fails = append(fails, "call missing or doubled")
				mu.Unlock()
				return
			}
			if len(sess.Calls) == 1 && len(inflight) == 1 && sess.Calls[0].ID == inflight[0].CallID {
				mu.Lock()
				fails = append(fails, "overlap")
				mu.Unlock()
				return
			}
		}
	}()
	time.Sleep(time.Millisecond)
	store.Finish("s", id, func(call *Call) {
		call.EndedAt = time.Now().UTC()
		call.HTTPStatus = 200
	})
	<-done
	if len(fails) > 0 {
		t.Fatal(fails)
	}
}

const httpMethodPost = "POST"

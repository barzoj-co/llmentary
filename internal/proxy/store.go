// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"
)

var ErrWaitTimeout = errors.New("wait_timeout")

type SessionSummary struct {
	ID             string     `json:"id"`
	Calls          int        `json:"calls"`
	Active         int        `json:"active"`
	Sources        []string   `json:"sources"`
	FirstStartedAt *time.Time `json:"first_started_at,omitempty"`
	LastEndedAt    *time.Time `json:"last_ended_at,omitempty"`
}

type Store interface {
	Begin(sessionID string, call Call) (int, string)
	Finish(sessionID, callID string, update func(*Call))
	Lookup(sessionID string) (Session, bool)
	List(source string) []SessionSummary
	Delete(sessionID string)
	WaitQuiet(ctx context.Context, sessionID string, quiet, timeout time.Duration, minCalls int) (Session, error)
}

type MemoryStore struct {
	mu       sync.Mutex
	sessions map[string]*sessionState
	nextID   uint64
}

type sessionState struct {
	id      string
	calls   []Call
	active  int
	version uint64
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{sessions: map[string]*sessionState{}}
}

func (s *MemoryStore) Begin(sessionID string, call Call) (int, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.state(sessionID)
	state.active++
	state.version++
	s.nextID++
	call.ID = makeCallID(s.nextID)
	call.SessionID = sessionID
	call.Seq = len(state.calls) + 1
	state.calls = append(state.calls, call)
	return call.Seq, call.ID
}

func (s *MemoryStore) Finish(sessionID, callID string, update func(*Call)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.state(sessionID)
	for i := range state.calls {
		if state.calls[i].ID == callID {
			update(&state.calls[i])
			break
		}
	}
	if state.active > 0 {
		state.active--
	}
	state.version++
}

func (s *MemoryStore) Lookup(sessionID string) (Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.sessions[sessionID]
	if !ok {
		return Session{}, false
	}
	return copySession(state), true
}

func (s *MemoryStore) List(source string) []SessionSummary {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SessionSummary, 0, len(s.sessions))
	for _, state := range s.sessions {
		sum := summarizeSession(state)
		if source != "" && !containsString(sum.Sources, source) {
			continue
		}
		out = append(out, sum)
	}
	sort.Slice(out, func(i, j int) bool {
		ti, tj := out[i].FirstStartedAt, out[j].FirstStartedAt
		switch {
		case ti == nil && tj == nil:
			return out[i].ID < out[j].ID
		case ti == nil:
			return false
		case tj == nil:
			return true
		case !ti.Equal(*tj):
			return ti.Before(*tj)
		default:
			return out[i].ID < out[j].ID
		}
	})
	return out
}

func (s *MemoryStore) Delete(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, sessionID)
}

func (s *MemoryStore) WaitQuiet(ctx context.Context, sessionID string, quiet, timeout time.Duration, minCalls int) (Session, error) {
	if quiet <= 0 {
		quiet = 500 * time.Millisecond
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if minCalls < 0 {
		minCalls = 0
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(minDuration(quiet/4, 100*time.Millisecond))
	defer ticker.Stop()

	var lastVersion uint64
	var lastExists bool
	quietSince := time.Now()
	for {
		s.mu.Lock()
		state, exists := s.sessions[sessionID]
		finished := 0
		active := 0
		version := uint64(0)
		if exists {
			active = state.active
			version = state.version
			finished = finishedCallCount(state)
		}
		if exists != lastExists || version != lastVersion || active > 0 {
			lastVersion = version
			lastExists = exists
			quietSince = time.Now()
		}
		if exists && finished >= minCalls && active == 0 && time.Since(quietSince) >= quiet {
			out := copySession(state)
			s.mu.Unlock()
			return out, nil
		}
		if !exists && minCalls == 0 && time.Since(quietSince) >= quiet {
			s.mu.Unlock()
			return Session{ID: sessionID}, ErrWaitTimeout
		}
		s.mu.Unlock()

		select {
		case <-ctx.Done():
			return Session{}, ctx.Err()
		case <-deadline.C:
			return s.lookupUnlockedCopy(sessionID), ErrWaitTimeout
		case <-ticker.C:
		}
	}
}

func (s *MemoryStore) lookupUnlockedCopy(sessionID string) Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	if state, ok := s.sessions[sessionID]; ok {
		return copySession(state)
	}
	return Session{ID: sessionID}
}

func finishedCallCount(state *sessionState) int {
	n := 0
	for i := range state.calls {
		if !state.calls[i].EndedAt.IsZero() {
			n++
		}
	}
	return n
}

func summarizeSession(state *sessionState) SessionSummary {
	sources := uniqueSources(state.calls)
	sum := SessionSummary{
		ID:      state.id,
		Calls:   len(state.calls),
		Active:  state.active,
		Sources: sources,
	}
	var first *time.Time
	var last *time.Time
	for i := range state.calls {
		c := &state.calls[i]
		if !c.StartedAt.IsZero() && (first == nil || c.StartedAt.Before(*first)) {
			t := c.StartedAt
			first = &t
		}
		if !c.EndedAt.IsZero() && (last == nil || c.EndedAt.After(*last)) {
			t := c.EndedAt
			last = &t
		}
	}
	sum.FirstStartedAt = first
	sum.LastEndedAt = last
	return sum
}

func uniqueSources(calls []Call) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, c := range calls {
		src := c.SessionSource
		if src == "" {
			continue
		}
		if _, ok := seen[src]; ok {
			continue
		}
		seen[src] = struct{}{}
		out = append(out, src)
	}
	sort.Strings(out)
	return out
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func (s *MemoryStore) state(sessionID string) *sessionState {
	if sessionID == "" {
		sessionID = DefaultSessionID
	}
	if state, ok := s.sessions[sessionID]; ok {
		return state
	}
	state := &sessionState{id: sessionID}
	s.sessions[sessionID] = state
	return state
}

func copySession(state *sessionState) Session {
	calls := make([]Call, len(state.calls))
	for i := range state.calls {
		calls[i] = cloneCall(state.calls[i])
	}
	return Session{ID: state.id, Calls: calls}
}

func cloneCall(call Call) Call {
	call.Request.Headers = cloneStringSlices(call.Request.Headers)
	call.Response.Headers = cloneStringSlices(call.Response.Headers)
	call.RequestIDs = cloneStringMap(call.RequestIDs)
	call.RawRequest = append([]byte(nil), call.RawRequest...)
	call.RawResponse = append([]byte(nil), call.RawResponse...)
	call.SSEEvents = append([]SSEEvent(nil), call.SSEEvents...)
	for i := range call.SSEEvents {
		call.SSEEvents[i].Data = append([]byte(nil), call.SSEEvents[i].Data...)
	}
	call.Normalized.ToolsDeclared = append([]ToolDecl(nil), call.Normalized.ToolsDeclared...)
	call.Normalized.ToolCalls = append([]ToolCall(nil), call.Normalized.ToolCalls...)
	for i := range call.Normalized.ToolCalls {
		call.Normalized.ToolCalls[i].Arguments = append([]byte(nil), call.Normalized.ToolCalls[i].Arguments...)
	}
	call.Normalized.ToolResultsIn = append([]ToolResult(nil), call.Normalized.ToolResultsIn...)
	call.Normalized.ParserWarnings = append([]string(nil), call.Normalized.ParserWarnings...)
	if call.Normalized.Usage != nil {
		usage := *call.Normalized.Usage
		usage.Raw = append([]byte(nil), usage.Raw...)
		call.Normalized.Usage = &usage
	}
	if call.Normalized.ContentFilter != nil {
		filter := *call.Normalized.ContentFilter
		filter.Categories = cloneFilterResults(filter.Categories)
		filter.Raw = append([]json.RawMessage(nil), filter.Raw...)
		for i := range filter.Raw {
			filter.Raw[i] = append([]byte(nil), filter.Raw[i]...)
		}
		call.Normalized.ContentFilter = &filter
	}
	if call.Normalized.Error != nil {
		errCopy := *call.Normalized.Error
		call.Normalized.Error = &errCopy
	}
	return call
}

func cloneStringSlices(in map[string][]string) map[string][]string {
	if in == nil {
		return nil
	}
	out := make(map[string][]string, len(in))
	for key, values := range in {
		out[key] = append([]string(nil), values...)
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneFilterResults(in map[string]FilterResult) map[string]FilterResult {
	if in == nil {
		return nil
	}
	out := make(map[string]FilterResult, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func makeCallID(n uint64) string {
	const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
	if n == 0 {
		return "call_0"
	}
	buf := make([]byte, 0, 16)
	for n > 0 {
		buf = append(buf, alphabet[n%uint64(len(alphabet))])
		n /= uint64(len(alphabet))
	}
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	return "call_" + string(buf)
}

func minDuration(a, b time.Duration) time.Duration {
	if a <= 0 {
		return b
	}
	if a < b {
		return a
	}
	return b
}

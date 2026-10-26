// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"sync"
	"time"
)

var (
	ErrWaitTimeout     = errors.New("wait_timeout")
	ErrSessionNotFound = errors.New("session_not_found")
)

type SpecCounters struct {
	Checked     int `json:"checked"`
	Clean       int `json:"clean"`
	Findings    int `json:"findings"`
	RequestOnly int `json:"request_only"`
	NotChecked  int `json:"not_checked"`
}

func (s SpecCounters) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]int{
		"checked":      s.Checked,
		"clean":        s.Clean,
		"findings":     s.Findings,
		"request_only": s.RequestOnly,
		"not_checked":  s.NotChecked,
		// Deprecated 0.x aliases (PascalCase dump a client printed).
		"Checked":     s.Checked,
		"Clean":       s.Clean,
		"Findings":    s.Findings,
		"RequestOnly": s.RequestOnly,
		"NotChecked":  s.NotChecked,
	})
}

func (s *SpecCounters) UnmarshalJSON(raw []byte) error {
	var keys map[string]int
	if err := json.Unmarshal(raw, &keys); err != nil {
		return err
	}
	s.Checked = firstInt(keys, "checked", "Checked")
	s.Clean = firstInt(keys, "clean", "Clean")
	s.Findings = firstInt(keys, "findings", "Findings")
	s.RequestOnly = firstInt(keys, "request_only", "RequestOnly")
	s.NotChecked = firstInt(keys, "not_checked", "NotChecked")
	return nil
}

func firstInt(keys map[string]int, names ...string) int {
	for _, name := range names {
		if v, ok := keys[name]; ok {
			return v
		}
	}
	return 0
}

type SessionSummary struct {
	ID             string       `json:"id"`
	Calls          int          `json:"calls"`
	Active         int          `json:"active"`
	Sources        []string     `json:"sources"`
	FirstStartedAt *time.Time   `json:"first_started_at,omitempty"`
	LastEndedAt    *time.Time   `json:"last_ended_at,omitempty"`
	Spec           SpecCounters `json:"spec"`
}

type Store interface {
	Begin(sessionID string, call Call) (int, string)
	Progress(sessionID, callID string, update func(*Call))
	Finish(sessionID, callID string, update func(*Call))
	Lookup(sessionID string) (Session, bool)
	InFlight(sessionID string) []InFlightCall
	SessionAndInFlight(sessionID string) (Session, []InFlightCall, bool)
	List(source string) []SessionSummary
	Delete(sessionID string)
	WaitQuiet(ctx context.Context, sessionID string, quiet, timeout time.Duration, minCalls int) (Session, error)
}

type MemoryStore struct {
	mu           sync.Mutex
	sessions     map[string]*sessionState
	nextID       uint64
	beforeFinish func()
}

// SetBeforeFinish registers a test hook invoked at the start of Finish,
// before the store lock is taken. Tests use it to observe GET/wait during
// the window after Begin and before the settled record is published.
// A nil hook clears it.
func (s *MemoryStore) SetBeforeFinish(fn func()) {
	s.mu.Lock()
	s.beforeFinish = fn
	s.mu.Unlock()
}

type sessionState struct {
	id           string
	calls        []Call
	active       int
	version      uint64
	lastChangeAt time.Time
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
	state.lastChangeAt = time.Now()
	s.nextID++
	call.ID = makeCallID(s.nextID)
	call.SessionID = sessionID
	call.Seq = len(state.calls) + 1
	state.calls = append(state.calls, call)
	return call.Seq, call.ID
}

func (s *MemoryStore) Progress(sessionID, callID string, update func(*Call)) {
	if update == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if sessionID == "" {
		sessionID = DefaultSessionID
	}
	state, ok := s.sessions[sessionID]
	if !ok {
		return
	}
	for i := range state.calls {
		if state.calls[i].ID == callID && !callSettled(state.calls[i]) {
			update(&state.calls[i])
			return
		}
	}
}

func (s *MemoryStore) InFlight(sessionID string) []InFlightCall {
	_, inFlight, ok := s.SessionAndInFlight(sessionID)
	if !ok {
		return nil
	}
	return inFlight
}

func (s *MemoryStore) SessionAndInFlight(sessionID string) (Session, []InFlightCall, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sessionID == "" {
		sessionID = DefaultSessionID
	}
	state, ok := s.sessions[sessionID]
	if !ok {
		return Session{ID: sessionID}, []InFlightCall{}, false
	}
	return copySession(state), inFlightSnapshot(state, time.Now().UTC()), true
}

func (s *MemoryStore) Finish(sessionID, callID string, update func(*Call)) {
	s.mu.Lock()
	hook := s.beforeFinish
	s.mu.Unlock()
	if hook != nil {
		hook()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if sessionID == "" {
		sessionID = DefaultSessionID
	}
	state, ok := s.sessions[sessionID]
	if !ok {
		return
	}
	for i := range state.calls {
		if state.calls[i].ID == callID {
			update(&state.calls[i])
			if !state.calls[i].EndedAt.IsZero() {
				state.calls[i].Outcome = classifyOutcome(state.calls[i])
			}
			if state.active > 0 {
				state.active--
			}
			state.version++
			state.lastChangeAt = time.Now()
			return
		}
	}
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

	quietSince := time.Now()
	wasPresent := false
	for {
		s.mu.Lock()
		state, exists := s.sessions[sessionID]
		finished := 0
		active := 0
		if exists {
			wasPresent = true
			active = state.active
			finished = finishedCallCount(state)
			if !state.lastChangeAt.IsZero() {
				quietSince = state.lastChangeAt
			}
		} else if wasPresent {
			quietSince = time.Now()
			wasPresent = false
		}
		if exists && finished >= minCalls && active == 0 && time.Since(quietSince) >= quiet {
			out := copySession(state)
			s.mu.Unlock()
			return out, nil
		}
		if !exists && minCalls == 0 {
			s.mu.Unlock()
			return Session{ID: sessionID}, ErrSessionNotFound
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

func callSettled(call Call) bool {
	return !call.EndedAt.IsZero()
}

func finishedCallCount(state *sessionState) int {
	n := 0
	for i := range state.calls {
		if callSettled(state.calls[i]) {
			n++
		}
	}
	return n
}

func summarizeSession(state *sessionState) SessionSummary {
	sources := uniqueSources(state.calls)
	sum := SessionSummary{
		ID:      state.id,
		Calls:   finishedCallCount(state),
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
	sum.Spec = specCounters(settledCalls(state))
	return sum
}

func inFlightSnapshot(state *sessionState, now time.Time) []InFlightCall {
	out := make([]InFlightCall, 0)
	for i := range state.calls {
		if callSettled(state.calls[i]) {
			continue
		}
		out = append(out, inFlightFromCall(state.calls[i], now))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out
}

func inFlightFromCall(call Call, now time.Time) InFlightCall {
	phase := InFlightPhaseAwaitingHeaders
	if call.HTTPStatus > 0 || call.FirstByteAt != nil {
		phase = InFlightPhaseStreaming
	}
	safeURL := sanitizeInFlightURL(call.Upstream.URL)
	path := ""
	if u, err := url.Parse(safeURL); err == nil {
		path = u.Path
	}
	item := InFlightCall{
		CallID:        call.ID,
		Seq:           call.Seq,
		Method:        call.Upstream.Method,
		Path:          path,
		APIFamily:     call.APIFamily,
		Upstream:      safeURL,
		StartedAt:     call.StartedAt,
		ElapsedMS:     0,
		Phase:         phase,
		BytesReceived: call.BytesReceived,
	}
	if !call.StartedAt.IsZero() {
		item.ElapsedMS = now.Sub(call.StartedAt).Milliseconds()
		if item.ElapsedMS < 0 {
			item.ElapsedMS = 0
		}
	}
	if call.HTTPStatus > 0 {
		item.HTTPStatus = call.HTTPStatus
	}
	return item
}

func settledCalls(state *sessionState) []Call {
	out := make([]Call, 0, finishedCallCount(state))
	for i := range state.calls {
		if callSettled(state.calls[i]) {
			out = append(out, state.calls[i])
		}
	}
	return out
}

func specCounters(calls []Call) SpecCounters {
	var out SpecCounters
	for i := range calls {
		info := calls[i].Normalized.Validation
		findings := hasWarnSpecFindings(calls[i])
		switch {
		case findings:
			out.Checked++
			out.Findings++
		case info != nil && info.RequestChecked && info.ResponseChecked:
			out.Checked++
			out.Clean++
		case info != nil && info.RequestChecked:
			out.RequestOnly++
		default:
			out.NotChecked++
		}
	}
	return out
}

func hasWarnSpecFindings(call Call) bool {
	if call.Normalized.SpecViolations == nil {
		return false
	}
	for _, violation := range *call.Normalized.SpecViolations {
		if isWarnSpecFinding(violation) {
			return true
		}
	}
	return false
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
	calls := make([]Call, 0, finishedCallCount(state))
	for i := range state.calls {
		if !callSettled(state.calls[i]) {
			continue
		}
		calls = append(calls, cloneCall(state.calls[i]))
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

package lab

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/barzoj-co/llm-test-lab/pkg/llmtestlab"
)

type Store interface {
	Begin(sessionID string, call llmtestlab.Call) (int, string)
	Finish(sessionID, callID string, update func(*llmtestlab.Call))
	Session(sessionID string) llmtestlab.Session
	Delete(sessionID string)
	WaitQuiet(ctx context.Context, sessionID string, quiet, timeout time.Duration) (llmtestlab.Session, error)
}

type MemoryStore struct {
	mu       sync.Mutex
	sessions map[string]*sessionState
	nextID   uint64
}

type sessionState struct {
	id      string
	calls   []llmtestlab.Call
	active  int
	version uint64
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{sessions: map[string]*sessionState{}}
}

func (s *MemoryStore) Begin(sessionID string, call llmtestlab.Call) (int, string) {
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

func (s *MemoryStore) Finish(sessionID, callID string, update func(*llmtestlab.Call)) {
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

func (s *MemoryStore) Session(sessionID string) llmtestlab.Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.state(sessionID)
	return copySession(state)
}

func (s *MemoryStore) Delete(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, sessionID)
}

func (s *MemoryStore) WaitQuiet(ctx context.Context, sessionID string, quiet, timeout time.Duration) (llmtestlab.Session, error) {
	if quiet <= 0 {
		quiet = 500 * time.Millisecond
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(minDuration(quiet/4, 100*time.Millisecond))
	defer ticker.Stop()

	var lastVersion uint64
	quietSince := time.Now()
	for {
		s.mu.Lock()
		state := s.state(sessionID)
		if state.version != lastVersion || state.active > 0 {
			lastVersion = state.version
			quietSince = time.Now()
		}
		if state.active == 0 && !quietSince.IsZero() && time.Since(quietSince) >= quiet {
			out := copySession(state)
			s.mu.Unlock()
			return out, nil
		}
		s.mu.Unlock()

		select {
		case <-ctx.Done():
			return llmtestlab.Session{}, ctx.Err()
		case <-deadline.C:
			return s.Session(sessionID), errors.New("wait quiet timeout")
		case <-ticker.C:
		}
	}
}

func (s *MemoryStore) state(sessionID string) *sessionState {
	if sessionID == "" {
		sessionID = llmtestlab.DefaultSessionID
	}
	if state, ok := s.sessions[sessionID]; ok {
		return state
	}
	state := &sessionState{id: sessionID}
	s.sessions[sessionID] = state
	return state
}

func copySession(state *sessionState) llmtestlab.Session {
	calls := append([]llmtestlab.Call(nil), state.calls...)
	return llmtestlab.Session{ID: state.id, Calls: calls}
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

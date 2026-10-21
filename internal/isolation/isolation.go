// SPDX-License-Identifier: AGPL-3.0-only

// Package isolation is the reference implementation of docs/isolation-spec.md.
package isolation

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/cases"
)

const DefaultMinChars = 8

type Options struct {
	AllowEmpty  bool
	MinChars    int
	IgnoreTexts []string
}

type Session struct {
	ID    string `json:"id"`
	Calls []Call `json:"calls"`
}

type Call struct {
	ID         string          `json:"id"`
	SessionID  string          `json:"session_id"`
	Seq        int             `json:"seq"`
	RawRequest json.RawMessage `json:"raw_request"`
}

type userTurn struct {
	Display    string
	Normalized string
	Seq        int
	CallID     string
}

func Check(x, y Session, opts Options) (bool, string) {
	if opts.MinChars <= 0 {
		opts.MinChars = DefaultMinChars
	}
	if len(x.Calls) == 0 {
		if opts.AllowEmpty {
			return true, ""
		}
		return false, fmt.Sprintf("no calls recorded for session %s", x.ID)
	}
	if len(y.Calls) == 0 {
		if opts.AllowEmpty {
			return true, ""
		}
		return false, fmt.Sprintf("no calls recorded for session %s (isolated_from needs calls on both sides)", y.ID)
	}
	for _, c := range x.Calls {
		if c.SessionID != x.ID {
			return false, fmt.Sprintf("call %s in session %s has session_id %s", c.ID, x.ID, c.SessionID)
		}
	}
	yIDs := map[string]struct{}{}
	for _, c := range y.Calls {
		if c.ID != "" {
			yIDs[c.ID] = struct{}{}
		}
	}
	for _, c := range x.Calls {
		if _, ok := yIDs[c.ID]; ok && c.ID != "" {
			return false, fmt.Sprintf("call id %s appears in both sessions", c.ID)
		}
	}

	ignore := map[string]struct{}{}
	for _, t := range opts.IgnoreTexts {
		if n := normalizeText(t); n != "" {
			ignore[n] = struct{}{}
		}
	}
	xIntro := introduced(x)
	yIntro := introduced(y)
	xIntroSet := map[string]userTurn{}
	for _, t := range xIntro {
		xIntroSet[t.Normalized] = t
	}
	yIntroSet := map[string]userTurn{}
	for _, t := range yIntro {
		yIntroSet[t.Normalized] = t
	}

	for _, c := range x.Calls {
		for _, turn := range userTurns(c) {
			if len([]rune(turn.Normalized)) < opts.MinChars {
				continue
			}
			if _, skip := ignore[turn.Normalized]; skip {
				continue
			}
			if _, own := xIntroSet[turn.Normalized]; own {
				continue
			}
			if foreign, ok := yIntroSet[turn.Normalized]; ok {
				return false, fmt.Sprintf(
					"user text '%s' also introduced by session %s #%d (%s) appears in session %s #%d (%s)",
					turn.Display, y.ID, foreign.Seq, foreign.CallID, x.ID, c.Seq, c.ID,
				)
			}
		}
	}
	return true, ""
}

func introduced(s Session) []userTurn {
	calls := append([]Call(nil), s.Calls...)
	sort.SliceStable(calls, func(i, j int) bool { return calls[i].Seq < calls[j].Seq })
	var out []userTurn
	seen := map[string]struct{}{}
	for i, c := range calls {
		turns := userTurns(c)
		for j, t := range turns {
			if t.Normalized == "" {
				continue
			}
			if i == 0 && j < len(turns)-1 {
				seen[t.Normalized] = struct{}{}
				continue
			}
			if _, ok := seen[t.Normalized]; ok {
				continue
			}
			seen[t.Normalized] = struct{}{}
			out = append(out, t)
		}
	}
	return out
}

func userTurns(c Call) []userTurn {
	body, err := decodeRawRequest(c.RawRequest)
	if err != nil || body == nil {
		return nil
	}
	var texts []string
	if msgs, ok := body["messages"].([]any); ok {
		for _, item := range msgs {
			m, _ := item.(map[string]any)
			if roleString(m["role"]) != "user" {
				continue
			}
			if t := contentText(m["content"]); t != "" {
				texts = append(texts, t)
			}
		}
	} else if in, ok := body["input"]; ok {
		switch v := in.(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				texts = append(texts, v)
			}
		case []any:
			for _, item := range v {
				m, _ := item.(map[string]any)
				if roleString(m["role"]) != "user" {
					continue
				}
				if t := contentText(m["content"]); t != "" {
					texts = append(texts, t)
				}
			}
		}
	}
	out := make([]userTurn, 0, len(texts))
	for _, t := range texts {
		display := strings.TrimSpace(t)
		out = append(out, userTurn{
			Display:    display,
			Normalized: normalizeText(t),
			Seq:        c.Seq,
			CallID:     c.ID,
		})
	}
	return out
}

func decodeRawRequest(raw json.RawMessage) (map[string]any, error) {
	raw = trimSpaceJSON(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, err
		}
		if b, err := base64.StdEncoding.DecodeString(s); err == nil {
			var out map[string]any
			if json.Unmarshal(b, &out) == nil {
				return out, nil
			}
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(s), &out); err != nil {
			return nil, err
		}
		return out, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func contentText(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		var parts []string
		for _, item := range t {
			m, _ := item.(map[string]any)
			if m == nil {
				continue
			}
			for _, key := range []string{"text", "input_text", "output_text"} {
				if s, ok := m[key].(string); ok && s != "" {
					parts = append(parts, s)
					break
				}
			}
		}
		return strings.Join(parts, "")
	default:
		return ""
	}
}

func roleString(v any) string {
	s, _ := v.(string)
	return strings.ToLower(strings.TrimSpace(s))
}

func normalizeText(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	fields := strings.FieldsFunc(s, unicode.IsSpace)
	s = strings.Join(fields, " ")
	return cases.Fold().String(s)
}

func trimSpaceJSON(raw json.RawMessage) json.RawMessage {
	return json.RawMessage(strings.TrimSpace(string(raw)))
}

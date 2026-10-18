// SPDX-License-Identifier: AGPL-3.0-only

package specvalidate

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

type precheckConfig struct {
	skip map[string]bool
}

func (c precheckConfig) enabled(keyword string) bool {
	return c.skip == nil || !c.skip[keyword]
}

func prechecks(raw []byte, profile string, cfg precheckConfig) []Violation {
	var out []Violation
	add := func(keyword, pointer, message string, offset *int) {
		if !cfg.enabled(keyword) {
			return
		}
		out = append(out, Violation{
			Side:    SideRequest,
			Source:  SourceSyntax,
			Path:    PointerToPath(pointer),
			Pointer: pointer,
			Keyword: keyword,
			Message: message,
			Profile: profile,
			Offset:  offset,
		})
	}

	if len(bytes.TrimSpace(raw)) == 0 {
		add("emptyBody", "", "body is empty", nil)
		return out
	}

	if bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}) {
		add("utf8BOM", "", "UTF-8 BOM prefix", intPtr(0))
		raw = raw[3:]
	}

	if !utf8.Valid(raw) {
		off := invalidUTF8Offset(raw)
		add("invalidUTF8", "", "body is not valid UTF-8", intPtr(off))
		return out
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var first any
	if err := dec.Decode(&first); err != nil {
		msg := err.Error()
		if strings.Contains(strings.ToLower(msg), "max depth") || strings.Contains(strings.ToLower(msg), "exceeded max") {
			add("maxDepth", "", "JSON nesting exceeds 128", intPtr(int(dec.InputOffset())))
			_ = walkRaw(raw, profile, cfg, &out)
			return out
		}
		add("trailingData", "", "body is not valid JSON: "+msg, intPtr(int(dec.InputOffset())))
		return out
	}

	restOff := int(dec.InputOffset())
	if restOff < 0 {
		restOff = 0
	}
	if restOff > len(raw) {
		restOff = len(raw)
	}
	rest := raw[restOff:]
	if i := indexNonWS(rest); i >= 0 {
		add("trailingData", "", "trailing data after the first JSON value", intPtr(restOff+i))
	}

	switch first.(type) {
	case map[string]any:
		// ok
	default:
		add("type", "", "top-level JSON value must be an object", nil)
	}

	_ = walkRaw(raw, profile, cfg, &out)
	return out
}

func walkRaw(raw []byte, profile string, cfg precheckConfig, out *[]Violation) error {
	s := rawScanner{data: raw, profile: profile, cfg: cfg, out: out}
	s.skipWS()
	if len(s.data) >= 3 && s.data[0] == 0xEF && s.data[1] == 0xBB && s.data[2] == 0xBF {
		s.pos = 3
		s.skipWS()
	}
	_, err := s.value("", 1)
	return err
}

type rawScanner struct {
	data    []byte
	pos     int
	profile string
	cfg     precheckConfig
	out     *[]Violation
}

func (s *rawScanner) add(keyword, pointer, message string, offset *int) {
	if !s.cfg.enabled(keyword) {
		return
	}
	*s.out = append(*s.out, Violation{
		Side:    SideRequest,
		Source:  SourceSyntax,
		Path:    PointerToPath(pointer),
		Pointer: pointer,
		Keyword: keyword,
		Message: message,
		Profile: s.profile,
		Offset:  offset,
	})
}

func (s *rawScanner) value(pointer string, depth int) (any, error) {
	s.skipWS()
	if s.pos >= len(s.data) {
		return nil, nil
	}
	if depth > MaxJSONDepth {
		s.add("maxDepth", pointer, "JSON nesting exceeds 128", intPtr(s.pos))
		s.skipValue()
		return nil, nil
	}
	switch s.data[s.pos] {
	case '{':
		return s.object(pointer, depth)
	case '[':
		return s.array(pointer, depth)
	case '"':
		return s.string()
	case 't', 'f', 'n':
		return s.literal()
	default:
		if s.data[s.pos] == '-' || (s.data[s.pos] >= '0' && s.data[s.pos] <= '9') {
			return s.number(pointer)
		}
	}
	return nil, nil
}

func (s *rawScanner) object(pointer string, depth int) (any, error) {
	s.pos++ // {
	seen := map[string]struct{}{}
	s.skipWS()
	if s.pos < len(s.data) && s.data[s.pos] == '}' {
		s.pos++
		return map[string]any{}, nil
	}
	for s.pos < len(s.data) {
		s.skipWS()
		key, err := s.string()
		if err != nil {
			return nil, err
		}
		child := joinPointer(pointer, key)
		if _, ok := seen[key]; ok {
			s.add("duplicateKey", child, "duplicate object key "+strconv.Quote(key), intPtr(s.pos))
		}
		seen[key] = struct{}{}
		s.skipWS()
		if s.pos < len(s.data) && s.data[s.pos] == ':' {
			s.pos++
		}
		if _, err := s.value(child, depth+1); err != nil {
			return nil, err
		}
		s.skipWS()
		if s.pos < len(s.data) && s.data[s.pos] == ',' {
			s.pos++
			continue
		}
		if s.pos < len(s.data) && s.data[s.pos] == '}' {
			s.pos++
			break
		}
		break
	}
	return nil, nil
}

func (s *rawScanner) array(pointer string, depth int) (any, error) {
	s.pos++ // [
	s.skipWS()
	if s.pos < len(s.data) && s.data[s.pos] == ']' {
		s.pos++
		return []any{}, nil
	}
	i := 0
	for s.pos < len(s.data) {
		child := pointer
		if depth <= MaxJSONDepth+2 {
			child = pointerIndex(pointer, i)
		}
		if _, err := s.value(child, depth+1); err != nil {
			return nil, err
		}
		i++
		s.skipWS()
		if s.pos < len(s.data) && s.data[s.pos] == ',' {
			s.pos++
			continue
		}
		if s.pos < len(s.data) && s.data[s.pos] == ']' {
			s.pos++
			break
		}
		break
	}
	return nil, nil
}

func (s *rawScanner) string() (string, error) {
	if s.pos >= len(s.data) || s.data[s.pos] != '"' {
		return "", nil
	}
	start := s.pos
	s.pos++
	for s.pos < len(s.data) {
		c := s.data[s.pos]
		if c == '\\' {
			s.pos += 2
			continue
		}
		if c == '"' {
			s.pos++
			var v string
			_ = json.Unmarshal(s.data[start:s.pos], &v)
			return v, nil
		}
		s.pos++
	}
	return "", nil
}

func (s *rawScanner) literal() (any, error) {
	for _, lit := range []string{"true", "false", "null"} {
		if s.pos+len(lit) <= len(s.data) && string(s.data[s.pos:s.pos+len(lit)]) == lit {
			s.pos += len(lit)
			return lit, nil
		}
	}
	return nil, nil
}

func (s *rawScanner) number(pointer string) (any, error) {
	start := s.pos
	if s.pos < len(s.data) && s.data[s.pos] == '-' {
		s.pos++
	}
	for s.pos < len(s.data) && s.data[s.pos] >= '0' && s.data[s.pos] <= '9' {
		s.pos++
	}
	if s.pos < len(s.data) && s.data[s.pos] == '.' {
		s.pos++
		for s.pos < len(s.data) && s.data[s.pos] >= '0' && s.data[s.pos] <= '9' {
			s.pos++
		}
	}
	if s.pos < len(s.data) && (s.data[s.pos] == 'e' || s.data[s.pos] == 'E') {
		s.pos++
		if s.pos < len(s.data) && (s.data[s.pos] == '+' || s.data[s.pos] == '-') {
			s.pos++
		}
		for s.pos < len(s.data) && s.data[s.pos] >= '0' && s.data[s.pos] <= '9' {
			s.pos++
		}
	}
	num := string(s.data[start:s.pos])
	if f, err := strconv.ParseFloat(num, 64); err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
		s.add("numberRange", pointer, "number is outside the float64 range", intPtr(start))
	}
	return num, nil
}

func (s *rawScanner) skipWS() {
	for s.pos < len(s.data) {
		switch s.data[s.pos] {
		case ' ', '\t', '\n', '\r':
			s.pos++
		default:
			return
		}
	}
}

func (s *rawScanner) skipValue() {
	s.skipWS()
	if s.pos >= len(s.data) {
		return
	}
	switch s.data[s.pos] {
	case '{', '[':
		s.skipContainer()
	case '"':
		_, _ = s.string()
	default:
		if s.data[s.pos] == '-' || (s.data[s.pos] >= '0' && s.data[s.pos] <= '9') {
			_, _ = s.number("")
			return
		}
		_, _ = s.literal()
	}
}

func (s *rawScanner) skipContainer() {
	// Iterative skip so a 100k-deep body cannot overflow the stack.
	if s.pos >= len(s.data) {
		return
	}
	start := s.data[s.pos]
	var end byte
	switch start {
	case '{':
		end = '}'
	case '[':
		end = ']'
	default:
		return
	}
	depth := 1
	s.pos++
	inStr := false
	esc := false
	for s.pos < len(s.data) && depth > 0 {
		c := s.data[s.pos]
		s.pos++
		if inStr {
			if esc {
				esc = false
				continue
			}
			if c == '\\' {
				esc = true
				continue
			}
			if c == '"' {
				inStr = false
			}
			continue
		}
		if c == '"' {
			inStr = true
			continue
		}
		if c == start {
			depth++
		} else if c == end {
			depth--
		} else if start == '{' && c == '[' {
			// stay in this skipper; nested arrays are counted only if we
			// started on an array. For mixed nesting, treat any opener as +1
			// against a generic depth using a stack-free pair count of all
			// brackets. The goal is only to advance pos past this value.
			_ = c
		}
	}
}

func indexNonWS(b []byte) int {
	for i, c := range b {
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			return i
		}
	}
	return -1
}

func invalidUTF8Offset(b []byte) int {
	for i := 0; i < len(b); {
		r, size := utf8.DecodeRune(b[i:])
		if r == utf8.RuneError && size == 1 {
			return i
		}
		i += size
	}
	return 0
}

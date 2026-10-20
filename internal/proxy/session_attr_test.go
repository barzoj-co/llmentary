// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"net/http"
	"strings"
	"testing"
)

func TestSplitSessionPrefix(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, path, escaped string
		id, rest            string
		present             bool
		wantErr             bool
	}{
		{name: "none", path: "/v1/chat/completions", rest: "/v1/chat/completions"},
		{name: "basic", path: "/s/t1/v1/chat/completions", id: "t1", rest: "/v1/chat/completions", present: true},
		{name: "with_u", path: "/s/t1/u/openrouter/chat/completions", id: "t1", rest: "/u/openrouter/chat/completions", present: true},
		{name: "space", path: "/s/test one/v1/x", escaped: "/s/test%20one/v1/x", id: "test one", rest: "/v1/x", present: true},
		{name: "empty", path: "/s//v1/x", present: true, wantErr: true},
		{name: "encoded_slash", path: "/s/%2F/v1/x", escaped: "/s/%2F/v1/x", present: true, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			esc := tc.escaped
			if esc == "" {
				esc = tc.path
			}
			id, rest, present, err := splitSessionPrefix(tc.path, esc)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if present != tc.present || id != tc.id || rest != tc.rest {
				t.Fatalf("id=%q rest=%q present=%v want id=%q rest=%q present=%v", id, rest, present, tc.id, tc.rest, tc.present)
			}
		})
	}
}

func TestParseSessionFromRejectsInvalid(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"", "foo", "header,foo"} {
		if _, err := ParseSessionFrom(raw); err == nil {
			t.Fatalf("%q: expected error", raw)
		}
	}
	got, err := ParseSessionFrom("path,header")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "path,header" {
		t.Fatalf("got %v", got)
	}
}

func TestResolveSessionPrecedence(t *testing.T) {
	t.Parallel()
	s := &Server{cfg: Config{SessionFrom: []string{"header", "path"}, DefaultSession: "default"}}
	req, err := http.NewRequest(http.MethodPost, "http://x/s/p/v1/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(SessionHeaderName, "h")
	got, err := s.resolveSession(req, "p", true)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "h" || got.Source != "header" {
		t.Fatalf("%+v", got)
	}
	s.cfg.SessionFrom = []string{"path", "header"}
	got, err = s.resolveSession(req, "p", true)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "p" || got.Source != "path" {
		t.Fatalf("%+v", got)
	}
}

func TestUnknownPathHint(t *testing.T) {
	t.Parallel()
	hint := unknownPathHint("/dogfood/v1/chat/completions")
	if !strings.Contains(hint, "/s/dogfood/v1/") {
		t.Fatalf("hint=%q", hint)
	}
}

func TestPropagateTraceValidate(t *testing.T) {
	t.Parallel()
	cfg := Config{ListenAddr: ":0", ControlAddr: "127.0.0.1:0", PropagateTrace: "always"}
	if err := cfg.AddUpstream("default=http://example.test"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "--propagate-trace") {
		t.Fatalf("err=%v", err)
	}
	cfg.PropagateTrace = PropagateTraceOff
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.PropagateTrace != PropagateTraceOff {
		t.Fatalf("default=%q", cfg.PropagateTrace)
	}
}

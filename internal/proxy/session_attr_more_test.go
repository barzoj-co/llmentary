// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseAllowPathPrefixes(t *testing.T) {
	t.Parallel()
	got := ParseAllowPathPrefixes(" Custom, /Other/, custom, nested/path, , / ")
	if strings.Join(got, ",") != "custom,other" {
		t.Fatalf("%v", got)
	}
	if ParseAllowPathPrefixes("") != nil && len(ParseAllowPathPrefixes("")) != 0 {
		t.Fatalf("%v", ParseAllowPathPrefixes(""))
	}
}

func TestValidateSessionIDCorners(t *testing.T) {
	t.Parallel()
	if err := validateSessionID(""); err == nil {
		t.Fatal("empty")
	}
	if err := validateSessionID(strings.Repeat("a", maxSessionIDBytes+1)); err == nil {
		t.Fatal("too long")
	}
	if err := validateSessionID("a/b"); err == nil {
		t.Fatal("slash")
	}
	if err := validateSessionID("a\x00b"); err == nil {
		t.Fatal("control")
	}
	if err := validateSessionID(string([]byte{0xff, 0xfe})); err == nil {
		t.Fatal("utf8")
	}
	if err := validateSessionID("ok-session"); err != nil {
		t.Fatal(err)
	}
}

func TestResolveSessionSourcesAndRequired(t *testing.T) {
	s := newAbortTestServer(t, NewMemoryStore())
	s.cfg.SessionFrom = []string{"header", "baggage", "path"}
	s.cfg.DefaultSession = ""

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	if _, err := s.resolveSession(req, "", false); err == nil || !strings.Contains(err.Error(), "session required") {
		t.Fatalf("required: %v", err)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set(SessionHeaderName, "a/b")
	if _, err := s.resolveSession(req, "", false); err == nil {
		t.Fatal("bad header id")
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("baggage", "llmentary.session.id=bag-1")
	info, err := s.resolveSession(req, "", false)
	if err != nil || info.ID != "bag-1" || info.Source != "baggage" {
		t.Fatalf("baggage %+v %v", info, err)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("baggage", "other=1, test-session = path-alt ")
	info, err = s.resolveSession(req, "", false)
	if err != nil || info.ID != "path-alt" {
		t.Fatalf("alt baggage %+v %v", info, err)
	}

	s.cfg.SessionFrom = nil
	req = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	info, err = s.resolveSession(req, "from-path", true)
	if err != nil || info.Source != "path" || info.ID != "from-path" {
		t.Fatalf("implicit sources %+v %v", info, err)
	}
}

func TestAllowedWirePathAndUnknownHint(t *testing.T) {
	t.Parallel()
	cfg := Config{AllowPathPrefixes: []string{"custom"}}
	if !cfg.allowedWirePath("/v1/chat/completions") || !cfg.allowedWirePath("/custom/x") {
		t.Fatal("allowed")
	}
	if cfg.allowedWirePath("/") || cfg.allowedWirePath("/secret/x") {
		t.Fatal("denied")
	}
	if !cfg.allowedWirePath("/beta/chat/completions") || !cfg.allowedWirePath("/v1beta/openai/chat/completions") {
		t.Fatal("beta prefixes")
	}
	if hint := unknownPathHint("/abc/v1/chat/completions"); hint != "/s/abc/v1/chat/completions" {
		t.Fatalf("hint=%q", hint)
	}
	if hint := unknownPathHint("/tokenize"); !strings.Contains(hint, "--allow-path-prefix tokenize") {
		t.Fatalf("allow hint=%q", hint)
	}
	if hint := unknownPathHint("/s/sid1/tokenize"); hint != "" {
		t.Fatalf("raw session path should not hint from first segment s: %q", hint)
	}
	if hint := unknownPathHint("/only"); hint == "" || !strings.Contains(hint, "--allow-path-prefix only") {
		t.Fatalf("short hint=%q", hint)
	}
	if hint := allowPathPrefixHint("/inference/v1/chat/completions"); !strings.Contains(hint, "--allow-path-prefix inference") {
		t.Fatalf("session-prefixed vendor path=%q", hint)
	}
	if hint := allowPathPrefixHint("/tokenize"); !strings.Contains(hint, "--allow-path-prefix tokenize") {
		t.Fatalf("session rest tokenize=%q", hint)
	}
	if hint := allowPathPrefixHint("/s/sid1/tokenize"); hint != "" {
		t.Fatalf("allow hint must not run session-typo logic: %q", hint)
	}
	if hint := allowPathPrefixHint("/"); hint != "" {
		t.Fatalf("empty rest=%q", hint)
	}
	if hint := unknownPathHint("/inference/v1/chat/completions"); hint != "/s/inference/v1/chat/completions" {
		t.Fatalf("unprefixed vendor path still session-typo: %q", hint)
	}
	if !hasNamedUpstreamPrefix("/u/llama/completion") || hasNamedUpstreamPrefix("/completion") {
		t.Fatal("named prefix")
	}
}

func TestDetectAPIFamilyBetaChatCompletions(t *testing.T) {
	t.Parallel()
	if got := detectAPIFamily(http.MethodPost, "/beta/chat/completions"); got != "openai.chat" {
		t.Fatalf("beta=%s", got)
	}
	if got := detectAPIFamily(http.MethodPost, "/v1beta/openai/chat/completions/"); got != "openai.chat" {
		t.Fatalf("v1beta=%s", got)
	}
	if got := detectAPIFamily(http.MethodGet, "/beta/chat/completions"); got != "passthrough" {
		t.Fatalf("GET=%s", got)
	}
	if got := detectAPIFamily(http.MethodPost, "/completion"); got != "passthrough" {
		t.Fatalf("native=%s", got)
	}
}

func TestStripRoutingAndFirstSegment(t *testing.T) {
	t.Parallel()
	if got := stripNamedUpstreamPath("/u/openai"); got != "/" {
		t.Fatalf("u only=%q", got)
	}
	if got := stripNamedUpstreamPath("/v1/x"); got != "/v1/x" {
		t.Fatalf("plain=%q", got)
	}
	if got := stripRoutingPrefixes("/u/openai/s/sid/v1/x"); got != "/v1/x" {
		t.Fatalf("nested=%q", got)
	}
	if firstPathSegment("/") != "" || firstPathSegment("/V1/x") != "v1" {
		t.Fatal("first segment")
	}
}

func TestSessionIDFromBaggageIgnoresJunk(t *testing.T) {
	t.Parallel()
	if sessionIDFromBaggage("no-eq, =empty, llmentary.session=") != "" {
		t.Fatal("junk")
	}
	if sessionIDFromBaggage("X-Test-Session=abc") != "abc" {
		t.Fatal("alias")
	}
}

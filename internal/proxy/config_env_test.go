// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"net/url"
	"strings"
	"testing"
)

func TestConfigFromEnvDefaultsAndOverrides(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("LLMENTARY_LISTEN", "127.0.0.1:7001")
	t.Setenv("LLMENTARY_CONTROL_LISTEN", "127.0.0.1:7002")
	t.Setenv("LLMENTARY_CONTROL_TOKEN", "tok")
	t.Setenv("LLMENTARY_SPEC_VALIDATION", "enforce")
	t.Setenv("LLMENTARY_SPEC_PROFILE", "strict")
	t.Setenv("LLMENTARY_SPEC_VALIDATION_MAX_BYTES", "4096")
	t.Setenv("LLMENTARY_SPEC_LOG_MAX", "0")
	t.Setenv("LLMENTARY_SESSION_FROM", "header,default")
	t.Setenv("LLMENTARY_ALLOW_PATH_PREFIXES", "custom, /Other/, custom, a/b,")
	t.Setenv("LLMENTARY_DEFAULT_SESSION", "sess-env")
	t.Setenv("LLMENTARY_PROPAGATE_TRACE", "auto")
	t.Setenv("LLMENTARY_UPSTREAMS", "openai=https://api.openai.com/v1,or=https://openrouter.ai/api/v1")

	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != "127.0.0.1:7001" || cfg.ControlAddr != "127.0.0.1:7002" || cfg.ControlToken != "tok" {
		t.Fatalf("addrs/token %+v", cfg)
	}
	if cfg.SpecValidation != "enforce" || cfg.SpecProfile != "strict" || cfg.SpecValidationMaxBytes != 4096 {
		t.Fatalf("spec %+v", cfg)
	}
	if cfg.SpecLogMax == nil || *cfg.SpecLogMax != 0 {
		t.Fatalf("spec log max=%v", cfg.SpecLogMax)
	}
	if strings.Join(cfg.SessionFrom, ",") != "header,default" {
		t.Fatalf("session-from=%v", cfg.SessionFrom)
	}
	if strings.Join(cfg.AllowPathPrefixes, ",") != "custom,other" {
		t.Fatalf("prefixes=%v", cfg.AllowPathPrefixes)
	}
	if cfg.DefaultSession != "sess-env" || cfg.PropagateTrace != "auto" {
		t.Fatalf("session/trace %+v", cfg)
	}
	if cfg.Upstreams["openai"] == nil || cfg.Upstreams["or"] == nil {
		t.Fatalf("upstreams=%v", cfg.Upstreams)
	}
}

func TestConfigFromEnvErrors(t *testing.T) {
	for _, tc := range []struct {
		name, key, val, want string
	}{
		{"bad_session_from", "LLMENTARY_SESSION_FROM", "nope", "session-from"},
		{"bad_max_bytes", "LLMENTARY_SPEC_VALIDATION_MAX_BYTES", "x", "spec-validation-max-bytes"},
		{"bad_log_max", "LLMENTARY_SPEC_LOG_MAX", "-1", "spec-log-max"},
		{"bad_upstream", "LLMENTARY_UPSTREAMS", "name=not-a-url", "scheme"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearConfigEnv(t)
			t.Setenv(tc.key, tc.val)
			_, err := ConfigFromEnv()
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.want)) && !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v want %q", err, tc.want)
			}
		})
	}
}

func TestConfigFromEnvSingularUpstream(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("LLMENTARY_UPSTREAM", "https://example.com/v1")
	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Upstreams["default"] == nil || cfg.Upstreams["default"].Host != "example.com" {
		t.Fatalf("upstreams=%v", cfg.Upstreams)
	}
}

func TestAddUpstreamCorners(t *testing.T) {
	t.Parallel()
	var cfg Config
	if err := cfg.AddUpstream("   "); err != nil {
		t.Fatal(err)
	}
	if err := cfg.AddUpstream("=https://x.example"); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("empty name: %v", err)
	}
	if err := cfg.AddUpstream("name=ftp://host"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.AddUpstream("name=/relative"); err == nil {
		t.Fatal("relative url")
	}
	if err := cfg.AddUpstream("name=https://%zz"); err == nil {
		t.Fatal("malformed upstream URL")
	}
}

func TestValidateSpecSettingsAndLoopback(t *testing.T) {
	t.Parallel()
	if err := validateSpecSettings("nope", "relaxed", 1, 1); err == nil {
		t.Fatal("mode")
	}
	if err := validateSpecSettings("warn", "nope", 1, 1); err == nil {
		t.Fatal("profile")
	}
	if err := validateSpecSettings("warn", "relaxed", -1, 1); err == nil {
		t.Fatal("max bytes")
	}
	if err := validateSpecSettings("off", "strict", 1, -2); err == nil {
		t.Fatal("log max")
	}
	if err := validateSpecSettings("off", "strict", 0, 0); err != nil {
		t.Fatal(err)
	}
	if !isLoopbackListenAddr("127.0.0.1:9") || !isLoopbackListenAddr("localhost") || !isLoopbackListenAddr("[::1]:8") {
		t.Fatal("loopback")
	}
	if isLoopbackListenAddr("10.0.0.1:9") || isLoopbackListenAddr("0.0.0.0:9") {
		t.Fatal("non-loopback")
	}
}

func TestApplyDefaultUpstreamNilAndFailure(t *testing.T) {
	t.Parallel()
	var nilCfg *Config
	if nilCfg.ApplyDefaultUpstream() {
		t.Fatal("nil config")
	}
	cfg := Config{Upstreams: map[string]*url.URL{}}
	if !cfg.ApplyDefaultUpstream() {
		t.Fatal("expected default")
	}
	if cfg.ApplyDefaultUpstream() {
		t.Fatal("second apply")
	}
}

func TestValidateRejectsBadDefaultSessionAndTrace(t *testing.T) {
	t.Parallel()
	cfg := Config{ListenAddr: "127.0.0.1:1", ControlAddr: "127.0.0.1:2", DefaultSession: "has/slash"}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "default-session") {
		t.Fatalf("session: %v", err)
	}
	cfg = Config{ListenAddr: "127.0.0.1:1", ControlAddr: "127.0.0.1:2", PropagateTrace: "maybe"}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "propagate-trace") {
		t.Fatalf("trace: %v", err)
	}
	if err := (*Config)(nil).Validate(); err == nil {
		t.Fatal("nil config")
	}
}

func TestIsSelfUpstreamEmptyAndHTTPSDefaultPort(t *testing.T) {
	t.Parallel()
	if isSelfUpstream("", mustURL(t, "http://127.0.0.1:80")) {
		t.Fatal("empty listen")
	}
	if isSelfUpstream("not-a-hostport", mustURL(t, "http://127.0.0.1:80")) {
		t.Fatal("bad listen")
	}
	if isSelfUpstream(":80", mustURL(t, "http://example.com")) {
		t.Fatal("example.com on :80 is not a self-loop")
	}
	if !isSelfUpstream(":443", mustURL(t, "https://localhost/v1")) {
		t.Fatal("https default 443 against :443 is a loop")
	}
	if isSelfUpstream(":9", nil) {
		t.Fatal("nil upstream")
	}
}

func TestGetenvFallback(t *testing.T) {
	t.Setenv("LLMENTARY_TEST_GETENV", "  value  ")
	if getenv("LLMENTARY_TEST_GETENV", "fb") != "value" {
		t.Fatal(getenv("LLMENTARY_TEST_GETENV", "fb"))
	}
	t.Setenv("LLMENTARY_TEST_GETENV", "   ")
	if getenv("LLMENTARY_TEST_GETENV", "fb") != "fb" {
		t.Fatal("whitespace should fall back")
	}
}

func TestOrDefaultAndSpecLogMaxValue(t *testing.T) {
	t.Parallel()
	if orDefault(" ", "x") != "x" || orDefault("y", "x") != "y" {
		t.Fatal("orDefault")
	}
	if specLogMaxValue(Config{}) != DefaultSpecLogMax {
		t.Fatal("nil spec log")
	}
	if specLogMaxValue(Config{SpecLogMax: IntPtr(0)}) != 0 {
		t.Fatal("zero spec log")
	}
}

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"LLMENTARY_LISTEN", "LLMENTARY_CONTROL_LISTEN", "LLMENTARY_CONTROL_TOKEN",
		"LLMENTARY_UPSTREAMS", "LLMENTARY_UPSTREAM", "LLMENTARY_SPEC_VALIDATION",
		"LLMENTARY_SPEC_PROFILE", "LLMENTARY_SPEC_VALIDATION_MAX_BYTES", "LLMENTARY_SPEC_LOG_MAX",
		"LLMENTARY_SPEC_TARGETS",
		"LLMENTARY_SESSION_FROM", "LLMENTARY_DEFAULT_SESSION",
		"LLMENTARY_ALLOW_PATH_PREFIXES", "LLMENTARY_PROPAGATE_TRACE",
	} {
		t.Setenv(key, "")
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

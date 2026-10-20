// SPDX-License-Identifier: AGPL-3.0-only

//go:build e2e

package llmentary_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/barzoj-co/llmentary/internal/proxy"
)

const liveRequestCap = 120

func checkLiveCost(used, cap int64) string {
	if used > cap {
		return fmt.Sprintf("K2 live cost guard: upstream calls=%d exceeds cap %d", used, cap)
	}
	return ""
}

func TestMain(m *testing.M) {
	before := proxy.UpstreamCallCount.Load()
	code := m.Run()
	used := proxy.UpstreamCallCount.Load() - before
	fmt.Fprintf(os.Stderr, "K2 live cost: upstream calls=%d cap=%d\n", used, liveRequestCap)
	if msg := checkLiveCost(used, int64(liveRequestCap)); msg != "" {
		fmt.Fprintln(os.Stderr, msg)
		os.Exit(1)
	}
	os.Exit(code)
}

type liveProxy struct {
	proxyURL   string
	controlURL string
}

func startLiveProxy(t *testing.T, mode, profile string) liveProxy {
	t.Helper()
	cfg := proxy.Config{
		ListenAddr:     ":0",
		ControlAddr:    "127.0.0.1:0",
		SpecValidation: mode,
		SpecProfile:    profile,
	}
	if err := cfg.AddUpstream("openai=https://api.openai.com/v1"); err != nil {
		t.Fatal(err)
	}
	server, err := proxy.NewServer(cfg, proxy.NewMemoryStore(), proxy.TolerantParser{})
	if err != nil {
		t.Fatal(err)
	}
	proxySrv := httptest.NewServer(server.ProxyHandler())
	t.Cleanup(proxySrv.Close)
	control := httptest.NewServer(server.ControlHandler())
	t.Cleanup(control.Close)
	return liveProxy{proxyURL: proxySrv.URL, controlURL: control.URL}
}

var liveSessionSeq atomic.Int64

func liveSession() string {
	return fmt.Sprintf("live-%d", liveSessionSeq.Add(1))
}

func liveModel() string {
	if model := os.Getenv("OPENAI_E2E_MODEL"); model != "" {
		return model
	}
	return "gpt-4.1-nano"
}

func postLive(t *testing.T, apiKey string, p liveProxy, path, session string, body []byte) (status int, hdr http.Header, resp []byte, sessionJSON map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, p.proxyURL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(proxy.SessionHeaderName, session)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	wait, err := http.NewRequestWithContext(context.Background(), http.MethodGet, p.controlURL+"/sessions/"+session+"/wait?quiet=50ms&timeout=30s", nil)
	if err != nil {
		t.Fatal(err)
	}
	wres, err := http.DefaultClient.Do(wait)
	if err != nil {
		t.Fatal(err)
	}
	wraw, err := io.ReadAll(wres.Body)
	_ = wres.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(wraw, &sessionJSON); err != nil {
		t.Fatalf("session json: %v", err)
	}
	return res.StatusCode, res.Header.Clone(), raw, sessionJSON
}

func liveLastCall(t *testing.T, session map[string]any) map[string]any {
	t.Helper()
	calls, _ := session["calls"].([]any)
	if len(calls) == 0 {
		t.Fatal("no calls")
	}
	call, _ := calls[len(calls)-1].(map[string]any)
	return call
}

func liveNormalized(call map[string]any) map[string]any {
	n, _ := call["normalized"].(map[string]any)
	return n
}

func liveSlice(v any) []any {
	s, _ := v.([]any)
	return s
}

func requestSideViolations(n map[string]any) []map[string]any {
	var out []map[string]any
	for _, v := range liveSlice(n["spec_violations"]) {
		m, _ := v.(map[string]any)
		if m["side"] == "response" {
			continue
		}
		out = append(out, m)
	}
	return out
}

type liveBehaviorFile struct {
	Retrieved  string                     `json:"retrieved"`
	Model      string                     `json:"model"`
	Rows       map[string]liveBehaviorRow `json:"rows"`
	TokenDelta struct {
		Epsilon         int `json:"epsilon"`
		MinControlDelta int `json:"min_control_delta"`
	} `json:"token_delta"`
}

type liveBehaviorRow struct {
	Test              string         `json:"test"`
	Status            int            `json:"status"`
	Error             map[string]any `json:"error"`
	ContentType       string         `json:"content_type"`
	RelaxedViolations bool           `json:"relaxed_violations"`
	StrictViolations  bool           `json:"strict_violations"`
	IgnoredKind       string         `json:"ignored_kind"`
	IgnoredFields     *bool          `json:"ignored_fields"`
	MidStream         *bool          `json:"mid_stream"`
	ContradictsStrict bool           `json:"contradicts_strict"`
	Overlay           string         `json:"overlay"`
}

func liveBehaviorRaw(t *testing.T) []byte {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "testdata", "live-behavior.json"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func loadLiveBehavior(t *testing.T) liveBehaviorFile {
	t.Helper()
	raw := liveBehaviorRaw(t)
	var out liveBehaviorFile
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func loadTokenBlock(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "testdata", "token_block.txt"))
	if err != nil {
		t.Fatal(err)
	}
	s := strings.TrimSpace(string(raw))
	if len(s) < 2000 {
		t.Fatalf("token block is %d bytes; want ≥ 2000", len(s))
	}
	return s
}

func assertLiveError(t *testing.T, row liveBehaviorRow, status int, body []byte) {
	t.Helper()
	if status != row.Status {
		t.Fatalf("OpenAI changed behavior: status=%d want %d. Update tests/e2e/testdata/live-behavior.json and the relax overlay. body=%s", status, row.Status, redactLiveBody(body))
	}
	if row.Error == nil {
		return
	}
	var env map[string]any
	_ = json.Unmarshal(body, &env)
	errObj, _ := env["error"].(map[string]any)
	if want, ok := row.Error["type"].(string); ok && want != "" && fmt.Sprint(errObj["type"]) != want {
		t.Fatalf("OpenAI changed behavior: error.type=%v want %s. Update live-behavior.json. body=%s", errObj["type"], want, redactLiveBody(body))
	}
	if want, ok := row.Error["code"]; ok {
		if !liveCodeEqual(errObj["code"], want) {
			t.Fatalf("OpenAI changed behavior: error.code=%v want %v. Update live-behavior.json.", errObj["code"], want)
		}
	}
	if want, ok := row.Error["param"].(string); ok && want != "" && fmt.Sprint(errObj["param"]) != want {
		t.Fatalf("OpenAI changed behavior: error.param=%v want %s. Update live-behavior.json.", errObj["param"], want)
	}
	if want, ok := row.Error["message_contains"].(string); ok && want != "" && !strings.Contains(fmt.Sprint(errObj["message"]), want) {
		t.Fatalf("OpenAI changed behavior: error.message=%v does not contain %q. Update live-behavior.json.", errObj["message"], want)
	}
}

func liveCodeEqual(got, want any) bool {
	if want == nil {
		return got == nil || fmt.Sprint(got) == "<nil>" || fmt.Sprint(got) == ""
	}
	return fmt.Sprint(got) == fmt.Sprint(want)
}

func redactLiveBody(body []byte) string {
	s := string(body)
	if len(s) > 400 {
		s = s[:400] + "…"
	}
	return s
}

func assertPrediction(t *testing.T, row liveBehaviorRow, relaxed, strict map[string]any, status int) {
	t.Helper()
	rel := requestSideViolations(relaxed)
	str := requestSideViolations(strict)
	if row.RelaxedViolations && len(rel) == 0 {
		t.Fatal("relaxed should have request-side spec_violations")
	}
	if !row.RelaxedViolations && len(rel) != 0 {
		t.Fatalf("relaxed should have no request-side spec_violations: %v", rel)
	}
	if row.RelaxedViolations != (status == 400) {
		t.Fatalf("C2 prediction failed: relaxed_violations=%v status=%d. Update live-behavior.json and the overlay.", row.RelaxedViolations, status)
	}
	if row.StrictViolations && len(str) == 0 {
		t.Fatal("strict should have request-side spec_violations")
	}
	if len(rel) > len(str) {
		t.Fatalf("strict violations should be a superset of relaxed (got relaxed=%d strict=%d)", len(rel), len(str))
	}
}

func quoteJSON(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

func assertIgnoredExact(t *testing.T, n map[string]any, want map[string]any) {
	t.Helper()
	var found map[string]any
	for _, v := range liveSlice(n["ignored_fields"]) {
		m, _ := v.(map[string]any)
		if m["key"] != want["key"] {
			continue
		}
		if p, ok := want["pointer"]; ok && p != "" && m["pointer"] != p {
			continue
		}
		found = m
		break
	}
	if found == nil {
		t.Fatalf("missing ignored_fields %v in %v", want, n["ignored_fields"])
	}
	for k, exp := range want {
		if fmt.Sprint(found[k]) != fmt.Sprint(exp) {
			t.Fatalf("ignored_fields.%s=%v want %v (entry=%v)", k, found[k], exp, found)
		}
	}
}

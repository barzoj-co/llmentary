// SPDX-License-Identifier: AGPL-3.0-only

package proxy_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/barzoj-co/llmentary/internal/proxy"
)

func compatGuide(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "openai-compatible.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestOpenAICompatibleGuidePinned(t *testing.T) {
	text := compatGuide(t)
	for _, n := range []string{
		"--upstream",
		"--spec-target",
		"--spec-validation",
		"--allow-path-prefix",
		"LLMENTARY_UPSTREAMS",
		"LLMENTARY_SPEC_TARGETS",
		"LLMENTARY_SPEC_VALIDATION",
		"LLMENTARY_ALLOW_PATH_PREFIXES",
		"name=openai",
		"name=openai-compatible",
		"api.deepseek.com",
		"https://api.deepseek.com",
		"https://api.deepseek.com/beta",
		"/beta/chat/completions",
		"/beta/completions",
		`"prefix": true`,
		"reasoning_content",
		"thinking",
		"https://api-docs.deepseek.com/guides/thinking_mode/",
		"https://api-docs.deepseek.com/api/create-chat-completion",
		"https://api-docs.deepseek.com/guides/chat_prefix_completion",
		"/v1beta/openai",
		"thought_signature",
		"reasoning_details",
		`source: "x_groq"`,
		"openai-compatible",
		"Response-side findings",
		"OPENAI_BASE_URL",
		"base_url",
		"baseURL",
		"unsupported_schema_keyword",
		"unknown_schema_keyword",
		"strict_fallback_predicted",
		"llmentary_unknown_path",
		"v1beta",
		"v1alpha",
		"top_logprobs",
		"Core known-field type",
		"Response-side `spec_violations`",
		"`strict_fallback` is the exception",
		"normalized.validation.target",
		"Passthrough calls",
	} {
		if !strings.Contains(text, n) {
			t.Errorf("docs/openai-compatible.md missing %q", n)
		}
	}
	stream := sectionAfter(text, "## Stream normalization")
	if stream == "" {
		t.Fatal("docs/openai-compatible.md missing ## Stream normalization")
	}
	for _, n := range []string{"`server`", "`rate_limit`", "`content_filter`", "`unknown`"} {
		if !strings.Contains(stream, n) {
			t.Errorf("stream section missing class %s", n)
		}
	}
	for _, n := range []string{"`auth`", "`bad_request`"} {
		if strings.Contains(stream, n) {
			t.Errorf("HTTP-200 stream error list must not include %s", n)
		}
	}
	for _, n := range []string{"agpl", "license"} {
		if strings.Contains(strings.ToLower(text), n) {
			t.Errorf("docs/openai-compatible.md must not contain %q", n)
		}
	}

	readme, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !hasMarkdownDest(string(readme), "docs/openai-compatible.md") {
		t.Fatal("README.md missing markdown link to docs/openai-compatible.md")
	}
	providers, err := os.ReadFile(filepath.Join("..", "..", "docs", "providers.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !hasMarkdownDest(string(providers), "openai-compatible.md") {
		t.Fatal("docs/providers.md missing markdown link to openai-compatible.md")
	}
}

func TestOpenAICompatibleGuideHelpFlags(t *testing.T) {
	cmd := exec.Command(proxyBinary(t), "--help")
	out, _ := cmd.CombinedOutput()
	help := string(out)
	if !strings.Contains(help, "spec-target") {
		t.Fatalf("--help missing spec-target: %s", help)
	}
	text := compatGuide(t)
	for _, flagName := range extractNeedles(text, "--", "abcdefghijklmnopqrstuvwxyz-") {
		if flagName == "--" {
			continue
		}
		if !strings.Contains(help, flagName+" ") && !strings.Contains(help, flagName+"\n") && !strings.Contains(help, "  "+flagName+" ") {
			// flag.FlagSet prints "  -name"
			dash := strings.TrimPrefix(flagName, "--")
			if !strings.Contains(help, "-"+dash) {
				t.Errorf("guide flag %s missing from --help", flagName)
			}
		}
	}
	for _, env := range []string{
		"LLMENTARY_UPSTREAMS",
		"LLMENTARY_SPEC_TARGETS",
		"LLMENTARY_SPEC_VALIDATION",
		"LLMENTARY_ALLOW_PATH_PREFIXES",
		"LLMENTARY_LISTEN",
		"LLMENTARY_CONTROL_LISTEN",
	} {
		if !strings.Contains(text, env) {
			t.Errorf("guide missing env %s", env)
		}
		if !strings.Contains(help, env) {
			t.Errorf("--help missing env %s", env)
		}
	}
}

func TestOpenAICompatibleGuideDeepSeekFakeUpstream(t *testing.T) {
	var mu sync.Mutex
	var lastPath string
	var lastBody []byte
	snapshot := func() (string, []byte) {
		mu.Lock()
		defer mu.Unlock()
		return lastPath, append([]byte(nil), lastBody...)
	}
	reset := func() {
		mu.Lock()
		lastPath = ""
		lastBody = nil
		mu.Unlock()
	}
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		lastPath = r.URL.Path
		lastBody = append([]byte(nil), body...)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(strings.TrimRight(r.URL.Path, "/"), "/chat/completions") {
			_, _ = w.Write([]byte(`{"id":"c","object":"chat.completion","created":1,"model":"deepseek-chat","choices":[{"index":0,"message":{"role":"assistant","content":"ok","reasoning_content":"think"},"finish_reason":"stop"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"cmpl","object":"text_completion","choices":[{"text":" pass","finish_reason":"stop"}]}`))
	}))
	t.Cleanup(fake.Close)

	proxyURL, controlURL, _ := startBinary(t, []string{
		"--upstream", "ds=" + fake.URL,
		"--spec-target", "ds=openai-compatible",
		"--spec-validation", "enforce",
	}, nil)

	cfgRes, err := http.Get(controlURL + "/config")
	if err != nil {
		t.Fatal(err)
	}
	cfgRaw, _ := io.ReadAll(cfgRes.Body)
	_ = cfgRes.Body.Close()
	if cfgRes.StatusCode != http.StatusOK {
		t.Fatalf("GET /config %d %s", cfgRes.StatusCode, cfgRaw)
	}
	var cfg map[string]any
	if err := json.Unmarshal(cfgRaw, &cfg); err != nil {
		t.Fatal(err)
	}
	targets, _ := cfg["spec_targets"].(map[string]any)
	if targets["ds"] != proxy.SpecTargetCompatible {
		t.Fatalf("spec_targets=%v", cfg["spec_targets"])
	}

	curlCfg := exec.Command("curl", "-sS", controlURL+"/config")
	curlOut, err := curlCfg.CombinedOutput()
	if err != nil {
		t.Fatalf("curl /config: %v %s", err, curlOut)
	}
	if !bytes.Contains(curlOut, []byte(`"ds"`)) || !bytes.Contains(curlOut, []byte(proxy.SpecTargetCompatible)) {
		t.Fatalf("curl /config missing spec target: %s", curlOut)
	}

	for i, block := range bashFences(compatGuide(t)) {
		if !strings.Contains(block, "curl ") || strings.Contains(block, "llmentary") {
			continue
		}
		script := strings.ReplaceAll(block, "http://127.0.0.1:7777", proxyURL)
		script = strings.ReplaceAll(script, "http://127.0.0.1:7778", controlURL)
		cmd := exec.Command("bash", "-c", script)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("guide bash curl %d: %v\n%s\n%s", i, err, script, out)
		}
		if strings.Contains(block, "max_tokens") && !strings.Contains(string(out), "400") {
			t.Fatalf("guide max_tokens curl: want 400, got %s", out)
		}
	}

	bodies := jsonFences(compatGuide(t))
	if len(bodies) < 5 {
		t.Fatalf("expected >=5 json fences, got %d", len(bodies))
	}

	type postCase struct {
		name   string
		path   string
		body   []byte
		want   int
		family string
	}
	var cases []postCase
	for i, raw := range bodies {
		compact := compactJSON(t, raw)
		want := http.StatusOK
		path := "/s/weather/u/ds/v1/chat/completions"
		family := "openai.chat"
		if bytes.Contains(compact, []byte(`"max_tokens": "16"`)) || bytes.Contains(compact, []byte(`"max_tokens":"16"`)) {
			want = http.StatusBadRequest
		}
		if bytes.Contains(compact, []byte(`"prefix"`)) {
			path = "/s/weather/u/ds/beta/chat/completions"
		}
		cases = append(cases, postCase{name: fmt.Sprintf("json_%d", i), path: path, body: compact, want: want, family: family})
	}
	cases = append(cases, postCase{
		name:   "beta_completions",
		path:   "/s/weather/u/ds/beta/completions",
		body:   []byte(`{"model":"deepseek-chat","prompt":"def hello():"}`),
		want:   http.StatusOK,
		family: "passthrough",
	}, postCase{
		name:   "top_logprobs_string",
		path:   "/s/weather/u/ds/v1/chat/completions",
		body:   []byte(`{"model":"deepseek-chat","messages":[{"role":"user","content":"hi"}],"top_logprobs":"5"}`),
		want:   http.StatusOK,
		family: "openai.chat",
	})

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reset()
			req, err := http.NewRequest(http.MethodPost, proxyURL+tc.path, bytes.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer sk-test")
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := io.ReadAll(res.Body)
			_ = res.Body.Close()
			if res.StatusCode != tc.want {
				t.Fatalf("status=%d want %d body=%s", res.StatusCode, tc.want, raw)
			}
			gotPath, gotBody := snapshot()
			if tc.want == http.StatusBadRequest {
				if gotPath != "" || len(gotBody) != 0 {
					t.Fatalf("type error reached upstream path=%q body=%s", gotPath, gotBody)
				}
				return
			}
			if !bytes.Equal(gotBody, tc.body) {
				t.Fatalf("upstream body mutated:\n got %s\nwant %s path=%s", gotBody, tc.body, gotPath)
			}
			sess := waitSession(t, controlURL, "weather")
			call := lastCall(t, sess)
			if fam, _ := call["api_family"].(string); fam != tc.family {
				t.Fatalf("api_family=%q want %q", fam, tc.family)
			}
			if tc.family == "openai.chat" && tc.want == http.StatusOK {
				n := normalized(call)
				val, _ := n["validation"].(map[string]any)
				if val["target"] != proxy.SpecTargetCompatible {
					t.Fatalf("target=%v", val)
				}
				if bytes.Contains(tc.body, []byte(`"reasoning_content"`)) {
					ri, _ := n["reasoning_in"].(map[string]any)
					if asIntValue(ri["assistant_turns"]) < 1 || asIntValue(ri["with_reasoning"]) < 1 {
						t.Fatalf("reasoning_in=%v", ri)
					}
				}
				if tc.name == "top_logprobs_string" && !hasRequestTypeWarning(n, "/top_logprobs") {
					t.Fatalf("missing request-side /top_logprobs type warning: %v", n["spec_violations"])
				}
			}
		})
	}

	prefixCurl := exec.Command("curl", "-sS", "-o", os.DevNull, "-w", "%{http_code}",
		proxyURL+"/s/weather/u/ds/beta/chat/completions",
		"-H", "Content-Type: application/json",
		"-H", "Authorization: Bearer sk-test",
		"-d", `{"model":"deepseek-chat","messages":[{"role":"user","content":"Write a Python hello world"},{"role":"assistant","content":"def hello():","prefix":true}]}`)
	code, err := prefixCurl.CombinedOutput()
	if err != nil {
		t.Fatalf("prefix curl: %v %s", err, code)
	}
	if strings.TrimSpace(string(code)) != "200" {
		t.Fatalf("prefix curl status=%s", code)
	}

	maxOut := filepath.Join(t.TempDir(), "max-tokens.out")
	maxCurl := exec.Command("curl", "-sS", "-o", maxOut, "-w", "%{http_code}",
		proxyURL+"/s/weather/u/ds/v1/chat/completions",
		"-H", "Content-Type: application/json",
		"-d", `{"model":"deepseek-chat","messages":[{"role":"user","content":"hi"}],"max_tokens":"16"}`)
	code, err = maxCurl.CombinedOutput()
	if err != nil {
		t.Fatalf("max_tokens curl: %v %s", err, code)
	}
	if strings.TrimSpace(string(code)) != "400" {
		t.Fatalf("max_tokens curl status=%s body=%s", code, mustRead(t, maxOut))
	}

	compCurl := exec.Command("curl", "-sS", "-o", os.DevNull, "-w", "%{http_code}",
		proxyURL+"/s/weather/u/ds/beta/completions",
		"-H", "Content-Type: application/json",
		"-H", "Authorization: Bearer sk-test",
		"-d", `{"model":"deepseek-chat","prompt":"def hello():"}`)
	code, err = compCurl.CombinedOutput()
	if err != nil {
		t.Fatalf("completions curl: %v %s", err, code)
	}
	if strings.TrimSpace(string(code)) != "200" {
		t.Fatalf("completions curl status=%s", code)
	}
	if path, _ := snapshot(); path != "/beta/completions" {
		t.Fatalf("completions upstream path=%q", path)
	}

	bareBeta := exec.Command("curl", "-sS", "-o", os.DevNull, "-w", "%{http_code}",
		proxyURL+"/beta/chat/completions",
		"-H", "Content-Type: application/json",
		"-d", `{"model":"deepseek-chat","messages":[{"role":"user","content":"hi"}]}`)
	code, err = bareBeta.CombinedOutput()
	if err != nil {
		t.Fatalf("bare beta curl: %v %s", err, code)
	}
	if strings.TrimSpace(string(code)) != "200" {
		t.Fatalf("bare beta status=%s", code)
	}
}

func TestOpenAICompatibleGuideEnvAndAllowPrefix(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"path":"` + r.URL.Path + `"}`))
	}))
	t.Cleanup(fake.Close)

	proxyURL, controlURL, _ := startBinary(t, nil, []string{
		"LLMENTARY_UPSTREAMS=ds=" + fake.URL,
		"LLMENTARY_SPEC_TARGETS=ds=openai-compatible",
		"LLMENTARY_SPEC_VALIDATION=enforce",
	})
	res, err := http.Get(controlURL + "/config")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if !bytes.Contains(raw, []byte(proxy.SpecTargetCompatible)) {
		t.Fatalf("env start config=%s", raw)
	}
	post := exec.Command("curl", "-sS", "-o", os.DevNull, "-w", "%{http_code}",
		proxyURL+"/s/weather/u/ds/v1/chat/completions",
		"-H", "Content-Type: application/json",
		"-d", `{"model":"deepseek-chat","messages":[{"role":"user","content":"hi"}]}`)
	code, err := post.CombinedOutput()
	if err != nil {
		t.Fatalf("env post: %v %s", err, code)
	}
	if strings.TrimSpace(string(code)) != "200" {
		t.Fatalf("env post status=%s", code)
	}

	allowURL, _, _ := startBinary(t, []string{
		"--upstream", "llama=" + fake.URL,
		"--allow-path-prefix", "tokenize",
	}, nil)
	tok := exec.Command("curl", "-sS", "-o", os.DevNull, "-w", "%{http_code}",
		allowURL+"/tokenize",
		"-H", "Content-Type: application/json",
		"-d", `{"prompt":"hi"}`)
	code, err = tok.CombinedOutput()
	if err != nil {
		t.Fatalf("tokenize curl: %v %s", err, code)
	}
	if strings.TrimSpace(string(code)) != "200" {
		t.Fatalf("tokenize status=%s", code)
	}

	denyURL, _, _ := startBinary(t, []string{"--upstream", "llama=" + fake.URL}, nil)
	noTok := exec.Command("curl", "-sS", "-o", os.DevNull, "-w", "%{http_code}",
		denyURL+"/tokenize",
		"-H", "Content-Type: application/json",
		"-d", `{}`)
	code, err = noTok.CombinedOutput()
	if err != nil && strings.TrimSpace(string(code)) == "" {
		t.Fatalf("denied tokenize: %v", err)
	}
	if strings.TrimSpace(string(code)) != "404" {
		t.Fatalf("denied tokenize status=%s", code)
	}
}

func TestOpenAICompatibleGuideClientSnippets(t *testing.T) {
	text := compatGuide(t)
	idx := strings.Index(text, "## Client examples")
	if idx < 0 {
		t.Fatal("missing Client examples")
	}
	section := text[idx:]
	for _, n := range []string{
		`os.Setenv("OPENAI_BASE_URL", "http://127.0.0.1:7777/s/weather/u/ds")`,
		`option.WithBaseURL("http://127.0.0.1:7777/s/weather/u/ds")`,
		`os.environ["OPENAI_BASE_URL"] = "http://127.0.0.1:7777/s/weather/u/ds"`,
		`OpenAI(base_url="http://127.0.0.1:7777/s/weather/u/ds")`,
		`process.env.OPENAI_BASE_URL = "http://127.0.0.1:7777/s/weather/u/ds";`,
		`baseURL: "http://127.0.0.1:7777/s/weather/u/ds"`,
		`OPENAI_BASE_URL=http://127.0.0.1:7777/s/weather/u/ds/beta`,
	} {
		if !strings.Contains(section, n) {
			t.Errorf("client section missing %q", n)
		}
	}
	for _, n := range []string{"X-Test-Session", "HTTPClient", "default_headers", "defaultHeaders"} {
		if strings.Contains(section, n) {
			t.Errorf("client section must only change base URL; found %q", n)
		}
	}
}

func TestOpenAICompatibleGuideCLIParsesProviderStarts(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(fake.Close)
	starts := []struct {
		args []string
		want map[string]string
	}{
		{
			args: []string{"--upstream", "ds=" + fake.URL},
			want: map[string]string{"ds": proxy.SpecTargetCompatible},
		},
		{
			args: []string{"--upstream", "openai=https://api.openai.com/v1"},
			want: map[string]string{"openai": proxy.SpecTargetOpenAI},
		},
		{
			args: []string{"--upstream", "gemini=" + fake.URL + "/v1beta/openai", "--spec-target", "gemini=openai-compatible", "--spec-validation", "enforce"},
			want: map[string]string{"gemini": proxy.SpecTargetCompatible},
		},
		{
			args: []string{"--upstream", "openrouter=" + fake.URL + "/api/v1", "--spec-target", "openrouter=openai-compatible", "--spec-validation", "enforce"},
			want: map[string]string{"openrouter": proxy.SpecTargetCompatible},
		},
		{
			args: []string{"--upstream", "groq=" + fake.URL + "/openai/v1", "--spec-target", "groq=openai-compatible", "--spec-validation", "enforce"},
			want: map[string]string{"groq": proxy.SpecTargetCompatible},
		},
		{
			args: []string{"--upstream", "mistral=" + fake.URL + "/v1", "--spec-target", "mistral=openai-compatible", "--spec-validation", "enforce"},
			want: map[string]string{"mistral": proxy.SpecTargetCompatible},
		},
		{
			args: []string{
				"--upstream", "ollama=" + fake.URL + "/v1",
				"--upstream", "llama=" + fake.URL,
				"--upstream", "vllm=" + fake.URL + "/v1",
				"--spec-target", "ollama=openai-compatible",
				"--spec-target", "llama=openai-compatible",
				"--spec-target", "vllm=openai-compatible",
				"--spec-validation", "enforce",
			},
			want: map[string]string{
				"ollama": proxy.SpecTargetCompatible,
				"llama":  proxy.SpecTargetCompatible,
				"vllm":   proxy.SpecTargetCompatible,
			},
		},
	}
	for i, tc := range starts {
		t.Run(fmt.Sprintf("start_%d", i), func(t *testing.T) {
			_, controlURL, _ := startBinary(t, tc.args, nil)
			res, err := http.Get(controlURL + "/config")
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := io.ReadAll(res.Body)
			_ = res.Body.Close()
			if res.StatusCode != 200 {
				t.Fatalf("config %d %s", res.StatusCode, raw)
			}
			var cfg struct {
				SpecTargets map[string]string `json:"spec_targets"`
			}
			if err := json.Unmarshal(raw, &cfg); err != nil {
				t.Fatal(err)
			}
			if len(cfg.SpecTargets) != len(tc.want) {
				t.Fatalf("spec_targets=%v want %v", cfg.SpecTargets, tc.want)
			}
			for name, want := range tc.want {
				if cfg.SpecTargets[name] != want {
					t.Fatalf("spec_targets=%v want %v", cfg.SpecTargets, tc.want)
				}
			}
		})
	}
}

func bashFences(text string) []string {
	var out []string
	rest := text
	for {
		start := strings.Index(rest, "```bash\n")
		if start < 0 {
			return out
		}
		rest = rest[start+len("```bash\n"):]
		end := strings.Index(rest, "```")
		if end < 0 {
			return out
		}
		out = append(out, strings.TrimSpace(rest[:end]))
		rest = rest[end+3:]
	}
}

func jsonFences(text string) [][]byte {
	var out [][]byte
	rest := text
	for {
		start := strings.Index(rest, "```json\n")
		if start < 0 {
			return out
		}
		rest = rest[start+len("```json\n"):]
		end := strings.Index(rest, "```")
		if end < 0 {
			return out
		}
		out = append(out, []byte(strings.TrimSpace(rest[:end])))
		rest = rest[end+3:]
	}
}

func compactJSON(t *testing.T, raw []byte) []byte {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("guide json: %v\n%s", err, raw)
	}
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func extractNeedles(text, prefix, alphabet string) []string {
	seen := map[string]bool{}
	var out []string
	for i := 0; i < len(text); {
		j := strings.Index(text[i:], prefix)
		if j < 0 {
			break
		}
		i += j
		n := len(prefix)
		for i+n < len(text) && strings.ContainsRune(alphabet, rune(text[i+n])) {
			n++
		}
		word := text[i : i+n]
		if !seen[word] {
			seen[word] = true
			out = append(out, word)
		}
		i += n
	}
	return out
}

func sectionAfter(text, heading string) string {
	idx := strings.Index(text, heading)
	if idx < 0 {
		return ""
	}
	rest := text[idx:]
	if next := strings.Index(rest[len(heading):], "\n## "); next >= 0 {
		return rest[:len(heading)+next]
	}
	return rest
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func hasRequestTypeWarning(n map[string]any, pointer string) bool {
	for _, raw := range asAnySlice(n["spec_violations"]) {
		v, _ := raw.(map[string]any)
		if fmtString(v["side"]) == "request" &&
			fmtString(v["pointer"]) == pointer &&
			fmtString(v["keyword"]) == "type" &&
			fmtString(v["severity"]) == "warning" {
			return true
		}
	}
	return false
}

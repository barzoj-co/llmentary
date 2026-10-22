// SPDX-License-Identifier: AGPL-3.0-only

package proxy_test

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"os/exec"
	"strings"
	"testing"

	"github.com/barzoj-co/llmentary/internal/proxy"
)

func withCompatTarget() func(*proxy.Config) {
	return func(cfg *proxy.Config) {
		if cfg.SpecTargets == nil {
			cfg.SpecTargets = map[string]string{}
		}
		cfg.SpecTargets["default"] = proxy.SpecTargetCompatible
	}
}

func TestSpecTargetConfigAndCLI(t *testing.T) {
	p := startSpecProxy(t, "enforce", "relaxed", chatOK(), withCompatTarget())
	cfg := getConfig(t, p.controlURL)
	targets, _ := cfg["spec_targets"].(map[string]any)
	if targets["default"] != proxy.SpecTargetCompatible {
		t.Fatalf("config spec_targets=%v", cfg["spec_targets"])
	}

	cmd := exec.Command(proxyBinary(t), "--spec-target=ollama=nope", "--upstream", "ollama=https://example.com")
	out, err := cmd.CombinedOutput()
	if err == nil || !bytes.Contains(out, []byte("allowed values")) {
		t.Fatalf("invalid target: err=%v out=%s", err, out)
	}
	cmd = exec.Command(proxyBinary(t), "--spec-target=missing=openai", "--upstream", "ollama=https://example.com")
	out, err = cmd.CombinedOutput()
	if err == nil || !bytes.Contains(out, []byte("unknown upstream")) {
		t.Fatalf("unknown name: err=%v out=%s", err, out)
	}
}

func TestSpecTargetVendorFieldsForwarded(t *testing.T) {
	type row struct {
		name   string
		route  string
		body   []byte
		needle string
	}
	chat := func(extra string) []byte {
		return []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],` + extra + `}`)
	}
	resp := func(extra string) []byte {
		return []byte(`{"model":"m","input":"hi",` + extra + `}`)
	}
	rows := []row{
		{"top_k", "/v1/chat/completions", chat(`"top_k":20`), "top_k"},
		{"min_p", "/v1/chat/completions", chat(`"min_p":0.1`), "min_p"},
		{"repetition_penalty", "/v1/chat/completions", chat(`"repetition_penalty":1.1`), "repetition_penalty"},
		{"chat_template_kwargs", "/v1/chat/completions", chat(`"chat_template_kwargs":{"enable_thinking":true}`), "chat_template_kwargs"},
		{"thinking", "/v1/chat/completions", chat(`"thinking":{"type":"enabled"}`), "thinking"},
		{"extra_body", "/v1/chat/completions", chat(`"extra_body":{"foo":1}`), "extra_body"},
		{"provider", "/v1/chat/completions", chat(`"provider":{"order":["groq"]}`), "provider"},
		{"usage", "/v1/chat/completions", chat(`"usage":{"include":true}`), "usage"},
		{"reasoning", "/v1/chat/completions", chat(`"reasoning":{"effort":"high"}`), "reasoning"},
		{"safe_prompt", "/v1/chat/completions", chat(`"safe_prompt":true`), "safe_prompt"},
		{"random_seed", "/v1/chat/completions", chat(`"random_seed":7`), "random_seed"},
		{"reasoning_format", "/v1/chat/completions", chat(`"reasoning_format":"parsed"`), "reasoning_format"},
		{"cache_prompt", "/v1/chat/completions", chat(`"cache_prompt":true`), "cache_prompt"},
		{"keep_alive", "/v1/chat/completions", chat(`"keep_alive":"5m"`), "keep_alive"},
		{"data_sources", "/v1/chat/completions", chat(`"data_sources":[{"type":"azure_search"}]`), "data_sources"},
		{"response_format_regex", "/v1/chat/completions", chat(`"response_format":{"type":"regex","pattern":".+"}`), "regex"},
		{"response_format_schema", "/v1/chat/completions", chat(`"response_format":{"type":"json_object","schema":{"type":"object"}}`), "schema"},
		{"tool_choice_any", "/v1/chat/completions", chat(`"tool_choice":"any","tools":[{"type":"function","function":{"name":"x","parameters":{"type":"object"}}}]`), "any"},
		{"stream_options_foo", "/v1/chat/completions", chat(`"stream_options":{"include_usage":true,"foo":1}`), "foo"},
		{"null_vendor", "/v1/chat/completions", chat(`"top_k":null`), "top_k"},
		{"resp_thinking", "/v1/responses", resp(`"thinking":true`), "thinking"},
		{"resp_reasoning", "/v1/responses", resp(`"reasoning":{"effort":"low","vendor":true}`), "vendor"},
	}
	for _, profile := range []string{"relaxed", "strict"} {
		for _, r := range rows {
			t.Run(profile+"/"+r.name, func(t *testing.T) {
				up := chatOK()
				if strings.Contains(r.route, "responses") {
					up = responsesOK()
				}
				p := startSpecProxy(t, "enforce", profile, up, withCompatTarget())
				status, _, _, sess := postRaw(t, p, r.route, newSession(), nil, r.body)
				if status != http.StatusOK {
					t.Fatalf("status=%d", status)
				}
				if p.upstream.Count() != 1 || !bytes.Contains(p.upstream.LastBody(), []byte(r.needle)) {
					t.Fatalf("not forwarded needle=%s body=%s", r.needle, p.upstream.LastBody())
				}
				n := normalized(lastCall(t, sess))
				val, _ := n["validation"].(map[string]any)
				if val["target"] != proxy.SpecTargetCompatible {
					t.Fatalf("target=%v", val["target"])
				}
			})
		}
	}
}

func TestSpecTargetOpenAIStillRejectsVendorFields(t *testing.T) {
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"top_k":20}`)
	for _, profile := range []string{"relaxed", "strict"} {
		p := startSpecProxy(t, "enforce", profile, chatOK())
		status, _, raw, _ := postRaw(t, p, "/v1/chat/completions", newSession(), nil, body)
		if status != http.StatusBadRequest {
			t.Fatalf("%s status=%d body=%s", profile, status, raw)
		}
		if p.upstream.Count() != 0 {
			t.Fatal("openai target called upstream")
		}
	}
}

func TestSpecTargetKnownTypeErrorsStillReject(t *testing.T) {
	p := startSpecProxy(t, "enforce", "relaxed", chatOK(), withCompatTarget())
	for _, body := range [][]byte{
		[]byte(`{"model":"m","messages":"hi"}`),
		[]byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"max_tokens":"16"}`),
	} {
		status, _, raw, _ := postRaw(t, p, "/v1/chat/completions", newSession(), nil, body)
		if status != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", status, raw)
		}
	}
	if p.upstream.Count() != 0 {
		t.Fatal("type errors reached upstream")
	}
}

func TestSpecTargetProviderRequiredMessageFields(t *testing.T) {
	long := strings.Repeat("r", 200000)
	rows := []struct {
		name string
		body []byte
	}{
		{"reasoning_content", []byte(`{"model":"m","messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"hello","reasoning_content":"user greets"},{"role":"user","content":"again"}]}`)},
		{"reasoning_details", []byte(`{"model":"m","messages":[{"role":"assistant","content":"x","reasoning_details":[{"type":"reasoning.summary","text":"n"}]}]}`)},
		{"prefix_last", []byte(`{"model":"m","messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"hel","prefix":true}]}`)},
		{"prefix_not_last", []byte(`{"model":"m","messages":[{"role":"assistant","content":"hel","prefix":true},{"role":"user","content":"ok"}]}`)},
		{"extra_content_google", []byte(`{"model":"m","messages":[{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"x","arguments":"{}"},"extra_content":{"google":{"thought_signature":"sig"}}}]}]}`)},
		{"extra_content_other", []byte(`{"model":"m","messages":[{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"x","arguments":"{}"},"extra_content":{"acme":{"tok":"t"}}}]}]}`)},
		{"reasoning_null", []byte(`{"model":"m","messages":[{"role":"assistant","content":"x","reasoning_content":null}]}`)},
		{"reasoning_empty", []byte(`{"model":"m","messages":[{"role":"assistant","content":"x","reasoning_content":""}]}`)},
		{"reasoning_long", []byte(`{"model":"m","messages":[{"role":"assistant","content":"x","reasoning_content":"` + long + `"}]}`)},
		{"user_reasoning_still_recorded", []byte(`{"model":"m","messages":[{"role":"user","content":"hi","reasoning_content":"nope"}]}`)},
		{"resp_input_vendor", []byte(`{"model":"m","input":[{"type":"message","role":"assistant","content":"x","reasoning_content":"y","foo":1}]}`)},
	}
	for _, profile := range []string{"relaxed", "strict"} {
		for _, r := range rows {
			t.Run(profile+"/"+r.name, func(t *testing.T) {
				route := "/v1/chat/completions"
				up := chatOK()
				if bytes.Contains(r.body, []byte(`"input"`)) {
					route = "/v1/responses"
					up = responsesOK()
				}
				p := startSpecProxy(t, "enforce", profile, up, withCompatTarget())
				status, _, raw, sess := postRaw(t, p, route, newSession(), nil, r.body)
				if status != http.StatusOK {
					t.Fatalf("status=%d body=%s", status, raw)
				}
				if p.upstream.Count() != 1 || !bytes.Equal(p.upstream.LastBody(), r.body) {
					t.Fatalf("body mutated or not forwarded")
				}
				n := normalized(lastCall(t, sess))
				if r.name == "reasoning_content" {
					ri, _ := n["reasoning_in"].(map[string]any)
					if asIntValue(ri["assistant_turns"]) < 1 || asIntValue(ri["with_reasoning"]) < 1 {
						t.Fatalf("reasoning_in=%v", ri)
					}
				}
			})
		}
	}

	p := startSpecProxy(t, "enforce", "relaxed", chatOK())
	status, _, raw, _ := postRaw(t, p, "/v1/chat/completions", newSession(), nil, []byte(`{"model":"m","messages":[{"role":"assistant","content":"x","reasoning_content":"y"}]}`))
	if status != http.StatusBadRequest {
		t.Fatalf("openai target should reject: %d %s", status, raw)
	}
	if !bytes.Contains(raw, []byte("OpenAI")) && !bytes.Contains(raw, []byte("ignored")) && !bytes.Contains(raw, []byte("unknown")) {
		t.Fatalf("hint/code missing: %s", raw)
	}
}

func TestSpecTargetPathFormsAndGzip(t *testing.T) {
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"top_k":3}`)
	p := startSpecProxy(t, "enforce", "relaxed", chatOK(), func(cfg *proxy.Config) {
		cfg.SpecTargets = map[string]string{"default": proxy.SpecTargetCompatible}
	})
	sid := newSession()
	status, _, _, _ := postRaw(t, p, "/s/"+sid+"/v1/chat/completions", sid, nil, body)
	if status != http.StatusOK {
		t.Fatalf("session path %d", status)
	}

	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write(body)
	_ = zw.Close()
	status, _, _, sess := postRaw(t, p, "/v1/chat/completions", newSession(), map[string]string{"Content-Encoding": "gzip"}, gz.Bytes())
	if status != http.StatusOK {
		t.Fatalf("gzip %d", status)
	}
	val, _ := normalized(lastCall(t, sess))["validation"].(map[string]any)
	if val["target"] != proxy.SpecTargetCompatible {
		t.Fatalf("gzip target=%v", val)
	}

	pNamed := startSpecProxy(t, "enforce", "relaxed", chatOK(), func(cfg *proxy.Config) {
		u := ""
		for name, up := range cfg.Upstreams {
			if name == "default" && up != nil {
				u = up.String()
			}
		}
		if u == "" {
			t.Fatal("missing default upstream")
		}
		if err := cfg.AddUpstream("ollama=" + u); err != nil {
			t.Fatal(err)
		}
		cfg.SpecTargets = map[string]string{"default": proxy.SpecTargetCompatible, "ollama": proxy.SpecTargetCompatible}
	})
	status, _, _, _ = postRaw(t, pNamed, "/u/ollama/v1/chat/completions", newSession(), nil, body)
	if status != http.StatusOK {
		t.Fatalf("named %d", status)
	}
	sid = newSession()
	status, _, _, _ = postRaw(t, pNamed, "/s/"+sid+"/u/ollama/v1/chat/completions", sid, nil, body)
	if status != http.StatusOK {
		t.Fatalf("session+named %d", status)
	}
}

func TestSpecTargetPrechecksUnchanged(t *testing.T) {
	p := startSpecProxy(t, "enforce", "relaxed", chatOK(), withCompatTarget())
	dup := []byte("{\n  \"model\": \"m\",\n  \"model\": \"n\",\n  \"messages\": [{\"role\":\"user\",\"content\":\"hi\"}]\n}")
	status, _, raw, _ := postRaw(t, p, "/v1/chat/completions", newSession(), nil, dup)
	if status != http.StatusBadRequest {
		t.Fatalf("duplicate keys should still reject: %d %s", status, raw)
	}
	bom := append([]byte{0xEF, 0xBB, 0xBF}, []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"top_k":1}`)...)
	p2 := startSpecProxy(t, "enforce", "relaxed", chatOK(), withCompatTarget())
	status, _, raw, _ = postRaw(t, p2, "/v1/chat/completions", newSession(), nil, bom)
	if status != http.StatusBadRequest {
		t.Fatalf("BOM still follows existing precheck: %d %s", status, raw)
	}
}

func asIntValue(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	default:
		return 0
	}
}

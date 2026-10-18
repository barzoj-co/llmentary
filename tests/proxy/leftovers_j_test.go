// SPDX-License-Identifier: AGPL-3.0-only

package proxy_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

var j1HeaderValues = map[string]string{
	"x-request-id":                   "req_j1",
	"openai-processing-ms":           "12",
	"openai-version":                 "2020-10-01",
	"openai-organization":            "org-fixture",
	"x-ratelimit-limit-requests":     "60",
	"x-ratelimit-remaining-requests": "59",
	"x-ratelimit-reset-requests":     "1s",
	"x-ratelimit-limit-tokens":       "10000",
	"x-ratelimit-remaining-tokens":   "9990",
	"x-ratelimit-reset-tokens":       "1s",
	"retry-after":                    "2",
	"retry-after-ms":                 "1500",
	"content-type":                   "application/json",
	"x-future-header":                "keep-me",
}

func TestJ1ErrorHeaderPassthrough(t *testing.T) {
	statuses := []int{400, 401, 403, 404, 429, 500, 503}
	families := []struct {
		name, path, req string
	}{
		{"chat", "/v1/chat/completions", `{"model":"m","messages":[{"role":"user","content":"hi"}]}`},
		{"responses", "/v1/responses", `{"model":"m","input":"hi"}`},
	}
	for _, fam := range families {
		for _, stream := range []bool{false, true} {
			mode := "nonstream"
			if stream {
				mode = "stream"
			}
			up := func(w http.ResponseWriter, r *http.Request) {
				status, _ := strconv.Atoi(r.Header.Get("X-Test-Status"))
				if status == 0 {
					status = 400
				}
				for k, v := range j1HeaderValues {
					w.Header().Set(k, v)
				}
				w.Header().Set("Connection", "keep-alive")
				w.Header().Set("Keep-Alive", "timeout=5")
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					w.WriteHeader(status)
					_, _ = io.WriteString(w, "data: {\"error\":{\"type\":\"server_error\",\"message\":\"nope\"}}\n\n")
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","code":"bad","message":"nope"}}`))
			}
			p := startSpecProxy(t, "off", "relaxed", up)
			for _, status := range statuses {
				t.Run(fmt.Sprintf("%s/%s/%d", fam.name, mode, status), func(t *testing.T) {
					sid := newSession()
					req := []byte(fam.req)
					if stream {
						req = []byte(strings.TrimSuffix(fam.req, "}") + `,"stream":true}`)
					}
					gotStatus, hdr, _, session := postRaw(t, p, fam.path, sid, map[string]string{
						"X-Test-Status": strconv.Itoa(status),
					}, req)
					if gotStatus != status {
						t.Fatalf("status=%d want %d", gotStatus, status)
					}
					for k, v := range j1HeaderValues {
						if k == "content-type" && stream {
							if !strings.Contains(hdr.Get("Content-Type"), "event-stream") {
								t.Fatalf("stream content-type=%q", hdr.Get("Content-Type"))
							}
							continue
						}
						if hdr.Get(k) != v {
							t.Fatalf("header %s=%q want %q", k, hdr.Get(k), v)
						}
					}
					if hdr.Get("Keep-Alive") == "timeout=5" {
						t.Fatal("Keep-Alive hop-by-hop header was forwarded")
					}
					if strings.EqualFold(hdr.Get("Connection"), "keep-alive") {
						t.Fatal("Connection hop-by-hop header was forwarded")
					}
					n := normalized(lastCall(t, session))
					errObj, _ := n["error"].(map[string]any)
					retry := fmt.Sprint(errObj["retry_after"])
					if retry != "1500" && retry != "2" {
						t.Fatalf("normalized.error.retry_after=%q err=%v", retry, errObj)
					}
				})
			}
		}
	}
}

func TestJ2RetryAfterVisibleAndClientRetry(t *testing.T) {
	t.Run("headers_visible_maxretries_0", func(t *testing.T) {
		up := func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "1")
			w.Header().Set("retry-after-ms", "1000")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"type":"rate_limit_error","message":"slow down"}}`))
		}
		p := startSpecProxy(t, "off", "relaxed", up)
		sid := newSession()
		status, hdr, _, _ := postRaw(t, p, "/v1/chat/completions", sid, nil, []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`))
		if status != 429 {
			t.Fatalf("status=%d", status)
		}
		if hdr.Get("Retry-After") != "1" {
			t.Fatalf("Retry-After=%q", hdr.Get("Retry-After"))
		}
		if hdr.Get("retry-after-ms") != "1000" {
			t.Fatalf("retry-after-ms=%q", hdr.Get("retry-after-ms"))
		}
		if p.upstream.Count() != 1 {
			t.Fatalf("MaxRetries(0) equivalent should be 1 call, got %d", p.upstream.Count())
		}
	})
	t.Run("client_retry_after_200ms", func(t *testing.T) {
		p := startSpecProxy(t, "off", "relaxed", func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Retry-Attempt") != "1" {
				w.Header().Set("Retry-After", "1")
				w.Header().Set("retry-after-ms", "1000")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"error":{"type":"rate_limit_error","message":"slow down"}}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"c","model":"gpt-fixture","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
		})
		const delay = 200 * time.Millisecond
		started := time.Now()
		sid := newSession()
		status1, _, _, _ := postRaw(t, p, "/v1/chat/completions", sid, nil, []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`))
		if status1 != 429 {
			t.Fatalf("first status=%d", status1)
		}
		time.Sleep(delay)
		status2, _, _, _ := postRaw(t, p, "/v1/chat/completions", sid, map[string]string{
			"X-Retry-Attempt": "1",
		}, []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`))
		elapsed := time.Since(started)
		if status2 != 200 {
			t.Fatalf("retry status=%d", status2)
		}
		if p.upstream.Count() != 2 {
			t.Fatalf("want 2 calls, got %d", p.upstream.Count())
		}
		if elapsed < delay-50*time.Millisecond {
			t.Fatalf("elapsed %s < delay-50ms", elapsed)
		}
	})
}

func TestJ3UnknownFieldsByteIdentity(t *testing.T) {
	t.Run("responses_nonstream_unknowns", func(t *testing.T) {
		resp := []byte(`{"id":"r","model":"m","status":"completed","future_root":1,"output":[{"id":"m","type":"message","role":"assistant","status":"completed","future_item":true,"content":[{"type":"output_text","text":"ok","annotations":[{"type":"url_citation","url":"https://x","future_ann":1}],"logprobs":[],"future_part":true}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2,"future_tokens":9,"input_tokens_details":{"cached_tokens":0,"future":1},"output_tokens_details":{"reasoning_tokens":0,"future":2}}}`)
		up := func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(resp)
		}
		req := []byte(`{"model":"m","input":[{"role":"user","content":[{"type":"input_text","text":"hi","future_part":true}],"future_item":1}],"tools":[{"type":"function","name":"x","parameters":{"type":"object","properties":{"a":{"type":"string","x-deep":1}},"extra_param":true},"future_tool":1}]}`)
		p := startSpecProxy(t, "warn", "strict", up)
		sid := newSession()
		_, _, client, session := postRaw(t, p, "/v1/responses", sid, nil, req)
		if !bytes.Equal(p.upstream.LastBody(), req) {
			t.Fatal("request bytes mutated")
		}
		if !bytes.Equal(client, resp) {
			t.Fatal("response bytes mutated")
		}
		n := normalized(lastCall(t, session))
		assertIgnoredKind(t, n, "unknown_request_field")
		foundInput := false
		for _, v := range asAnySlice(n["spec_violations"]) {
			m := v.(map[string]any)
			if m["side"] == "request" && (strings.Contains(fmt.Sprint(m["pointer"]), "/input/") || strings.Contains(fmt.Sprint(m["path"]), "input[")) {
				foundInput = true
			}
		}
		if !foundInput {
			t.Fatalf("input item extra should be spec_violation: %v", n["spec_violations"])
		}
	})
	t.Run("responses_relaxed_input_item_still_violation", func(t *testing.T) {
		req := []byte(`{"model":"m","input":[{"role":"user","content":"hi","foo":1}]}`)
		p := startSpecProxy(t, "warn", "relaxed", responsesOK())
		sid := newSession()
		_, _, _, session := postRaw(t, p, "/v1/responses", sid, nil, req)
		if requestViolations(normalized(lastCall(t, session))) == 0 {
			t.Fatalf("relaxed input item extra should still violate: %v", normalized(lastCall(t, session))["spec_violations"])
		}
	})
	t.Run("chat_message_and_tool_ignored", func(t *testing.T) {
		req := []byte(`{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"hi","future_part":1}],"foo":1}],"tools":[{"type":"function","function":{"name":"x","parameters":{"type":"object","properties":{"a":{"type":"string"}},"extra_param":true},"foo":1}}]}`)
		p := startSpecProxy(t, "warn", "relaxed", chatOK())
		sid := newSession()
		_, _, _, session := postRaw(t, p, "/v1/chat/completions", sid, nil, req)
		if !bytes.Equal(p.upstream.LastBody(), req) {
			t.Fatal("request bytes mutated")
		}
		n := normalized(lastCall(t, session))
		assertIgnoredKind(t, n, "unknown_request_field")
	})
	t.Run("chat_include_usage_future", func(t *testing.T) {
		stream := "data: {\"id\":\"c\",\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n" +
			"data: {\"id\":\"c\",\"choices\":[],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2,\"future_tokens\":9,\"prompt_tokens_details\":{\"cached_tokens\":0,\"future\":1}}}\n\n" +
			"data: [DONE]\n\n"
		up := func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, stream)
		}
		req := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"stream":true,"stream_options":{"include_usage":true}}`)
		p := startSpecProxy(t, "warn", "strict", up)
		sid := newSession()
		_, _, client, _ := postRaw(t, p, "/v1/chat/completions", sid, nil, req)
		if !bytes.Equal(client, []byte(stream)) {
			t.Fatal("stream bytes mutated")
		}
		if !bytes.Contains(client, []byte("future_tokens")) || !bytes.Contains(client, []byte(`"future"`)) {
			t.Fatal("usage extras stripped")
		}
	})
	t.Run("responses_completed_usage_unknown", func(t *testing.T) {
		completed := `{"type":"response.completed","sequence_number":1,"response":{"id":"r","created_at":1,"model":"m","object":"response","output":[],"parallel_tool_calls":true,"status":"completed","tool_choice":"auto","tools":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2,"future_tokens":3,"input_tokens_details":{"cached_tokens":0,"future":1}}}}`
		stream := "event: response.completed\ndata: " + completed + "\n\n"
		up := func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, stream)
		}
		req := []byte(`{"model":"m","input":"hi","stream":true}`)
		p := startSpecProxy(t, "warn", "strict", up)
		sid := newSession()
		_, _, client, _ := postRaw(t, p, "/v1/responses", sid, nil, req)
		if !bytes.Equal(client, []byte(stream)) {
			t.Fatal("completed bytes mutated")
		}
	})
}

func TestJ4ByteIdentityFixtures(t *testing.T) {
	type fx struct {
		name    string
		path    string
		req     []byte
		resp    []byte
		sse     bool
		headers map[string]string
	}
	chatReq := []byte("{\"model\":\"m\",\"messages\":[{\"role\":\"user\",\"content\":\"caf\\u00e9\"}]}\r\n")
	pretty := []byte("{\n  \"id\": \"c\",\n  \"model\": \"m\",\n  \"usage\": {\"prompt_tokens\": 1, \"completion_tokens\": 1, \"total_tokens\": 2},\n  \"choices\": [{\"message\": {\"role\": \"assistant\", \"content\": \"ok\"}, \"finish_reason\": \"stop\"}]\n}")
	usageFirst := []byte(`{"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2},"id":"c","model":"m","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	sse := []byte("data: {\"choices\":[{\"delta\":{\"content\":\"é\"}}]}\r\n\r\ndata: [DONE]\r\n\r\n")
	fixtures := []fx{
		{"crlf_json", "/v1/chat/completions", chatReq, []byte("{\"id\":\"c\",\"model\":\"m\",\"choices\":[{\"message\":{\"role\":\"assistant\",\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\r\n"), false, nil},
		{"crlf_sse", "/v1/chat/completions", []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"stream":true}`), sse, true, nil},
		{"usage_before_choices", "/v1/chat/completions", []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`), usageFirst, false, nil},
		{"pretty_whitespace", "/v1/chat/completions", []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`), pretty, false, nil},
		{"unicode_escape_vs_raw", "/v1/chat/completions", []byte(`{"model":"m","messages":[{"role":"user","content":"caf\u00e9"}]}`), []byte(`{"id":"c","model":"m","choices":[{"message":{"role":"assistant","content":"café"},"finish_reason":"stop"}]}`), false, nil},
	}
	for _, f := range fixtures {
		t.Run(f.name, func(t *testing.T) {
			sent := append([]byte(nil), f.resp...)
			up := func(w http.ResponseWriter, _ *http.Request) {
				if f.sse {
					w.Header().Set("Content-Type", "text/event-stream")
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write(sent)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(sent)
			}
			p := startSpecProxy(t, "warn", "relaxed", up)
			sid := newSession()
			_, _, client, session := postRaw(t, p, f.path, sid, f.headers, f.req)
			if sha256Hex(client) != sha256Hex(sent) {
				t.Fatalf("client-received != upstream-sent\nclient=%q\nsent=%q", client, sent)
			}
			if sha256Hex(p.upstream.LastBody()) != sha256Hex(f.req) {
				t.Fatalf("upstream-received != client-sent")
			}
			call := lastCall(t, session)
			rawReq := decodeB64(t, call["raw_request"])
			rawResp := decodeB64(t, call["raw_response"])
			if sha256Hex(rawReq) != sha256Hex(f.req) {
				t.Fatalf("raw_request hash mismatch")
			}
			if sha256Hex(rawResp) != sha256Hex(sent) {
				t.Fatalf("raw_response hash mismatch")
			}
		})
	}
}

func decodeB64(t *testing.T, v any) []byte {
	t.Helper()
	switch x := v.(type) {
	case string:
		out, err := base64.StdEncoding.DecodeString(x)
		if err != nil {
			t.Fatal(err)
		}
		return out
	case []byte:
		return x
	case json.RawMessage:
		var s string
		if json.Unmarshal(x, &s) == nil {
			out, err := base64.StdEncoding.DecodeString(s)
			if err == nil {
				return out
			}
		}
		return x
	default:
		t.Fatalf("raw field type %T", v)
		return nil
	}
}

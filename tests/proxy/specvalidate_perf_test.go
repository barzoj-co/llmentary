// SPDX-License-Identifier: AGPL-3.0-only

//go:build perf

package proxy_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"testing"
	"time"

	"github.com/barzoj-co/llm-test-lab/internal/lab"
	"github.com/barzoj-co/llm-test-lab/internal/schemalint"
	"github.com/barzoj-co/llm-test-lab/internal/specvalidate"
)

func compileEngine(t testing.TB) (*specvalidate.Engine, *schemalint.Linter) {
	t.Helper()
	eng, err := specvalidate.CompileEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	subset, err := schemalint.LoadSubset(specvalidate.EmbeddedSchemaSubset)
	if err != nil {
		t.Fatal(err)
	}
	return eng, schemalint.New(subset)
}

func typicalChatBody() []byte {
	msgs := make([]any, 0, 10)
	for i := 0; i < 10; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		msgs = append(msgs, map[string]any{"role": role, "content": fmt.Sprintf("message %d about weather in city %d", i, i)})
	}
	tools := make([]any, 0, 5)
	for i := 0; i < 5; i++ {
		tools = append(tools, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": fmt.Sprintf("tool_%d", i),
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"q":    map[string]any{"type": "string"},
						"n":    map[string]any{"type": "integer"},
						"flag": map[string]any{"type": "boolean"},
					},
					"required":             []any{"q"},
					"additionalProperties": false,
				},
			},
		})
	}
	body, _ := json.Marshal(map[string]any{"model": "m", "messages": msgs, "tools": tools})
	return body
}

func largeToolsBody(tools, props int) []byte {
	list := make([]any, 0, tools)
	for i := 0; i < tools; i++ {
		p := make(map[string]any, props)
		req := make([]any, 0, props)
		for j := 0; j < props; j++ {
			name := fmt.Sprintf("f%d", j)
			p[name] = map[string]any{"type": "string"}
			req = append(req, name)
		}
		list = append(list, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": fmt.Sprintf("t%d", i),
				"parameters": map[string]any{
					"type": "object", "properties": p, "required": req, "additionalProperties": false,
				},
			},
		})
	}
	body, _ := json.Marshal(map[string]any{
		"model":    "m",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
		"tools":    list,
	})
	return body
}

func deepWideSchemaBody() []byte {
	schema := measuredNesting(10)
	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		props = map[string]any{}
		schema["properties"] = props
	}
	req, _ := schema["required"].([]any)
	for i := 0; i < 5000; i++ {
		name := fmt.Sprintf("w%d", i)
		props[name] = map[string]any{"type": "string"}
		req = append(req, name)
	}
	schema["required"] = req
	return strictToolBody(schema)
}

func p99Micros(samples []time.Duration) time.Duration {
	cp := append([]time.Duration(nil), samples...)
	for i := 0; i < len(cp); i++ {
		for j := i + 1; j < len(cp); j++ {
			if cp[j] < cp[i] {
				cp[i], cp[j] = cp[j], cp[i]
			}
		}
	}
	idx := int(float64(len(cp)-1) * 0.99)
	if idx < 0 {
		idx = 0
	}
	return cp[idx]
}

func timeValidate(eng *specvalidate.Engine, lint *schemalint.Linter, body []byte, n int) []time.Duration {
	out := make([]time.Duration, n)
	for i := 0; i < n; i++ {
		start := time.Now()
		_ = eng.ValidateRequest(specvalidate.ProfileRelaxed, "openai.chat", body, 0)
		_ = lint.LintRequest("openai.chat", body)
		out[i] = time.Since(start)
	}
	return out
}

func TestI2TypicalP99(t *testing.T) {
	eng, lint := compileEngine(t)
	body := typicalChatBody()
	_ = timeValidate(eng, lint, body, 50)
	samples := timeValidate(eng, lint, body, 2000)
	p99 := p99Micros(samples)
	if p99 > 5*time.Millisecond {
		t.Fatalf("typical p99=%s want <5ms", p99)
	}
}

func BenchmarkTypicalValidation(b *testing.B) {
	eng, lint := compileEngine(b)
	body := typicalChatBody()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = eng.ValidateRequest(specvalidate.ProfileRelaxed, "openai.chat", body, 0)
		_ = lint.LintRequest("openai.chat", body)
	}
}

func TestI3LargeSchema(t *testing.T) {
	eng, lint := compileEngine(t)
	wide := largeToolsBody(20, 100)
	_ = timeValidate(eng, lint, wide, 20)
	wideSamples := timeValidate(eng, lint, wide, 400)
	if p99 := p99Micros(wideSamples); p99 > 25*time.Millisecond {
		t.Fatalf("20x100 p99=%s want <25ms", p99)
	}
	deep := deepWideSchemaBody()
	_ = timeValidate(eng, lint, deep, 10)
	deepSamples := timeValidate(eng, lint, deep, 80)
	if p99 := p99Micros(deepSamples); p99 > 100*time.Millisecond {
		t.Fatalf("5000-property depth-10 p99=%s want <100ms", p99)
	}
}

func BenchmarkLargeSchema(b *testing.B) {
	eng, lint := compileEngine(b)
	body := largeToolsBody(20, 100)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = eng.ValidateRequest(specvalidate.ProfileRelaxed, "openai.chat", body, 0)
		_ = lint.LintRequest("openai.chat", body)
	}
}

func TestI4Overhead(t *testing.T) {
	start := time.Now()
	if _, err := specvalidate.CompileEmbedded(); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("CompileEmbedded=%s want <2s", took)
	}
	body := typicalChatBody()
	pWarn := startSpecProxy(t, "warn", "relaxed", chatOK())
	pOff := startSpecProxy(t, "off", "relaxed", chatOK())
	measure := func(p proxyProc, n int) []time.Duration {
		out := make([]time.Duration, 0, n)
		for i := 0; i < n; i++ {
			sid := newSession()
			st := time.Now()
			_, _, _, _ = postRaw(t, p, "/v1/chat/completions", sid, nil, body)
			out = append(out, time.Since(st))
		}
		return out
	}
	_ = measure(pWarn, 20)
	_ = measure(pOff, 20)
	warn := measure(pWarn, 80)
	off := measure(pOff, 80)
	delta := p99Micros(warn) - p99Micros(off)
	if delta < 0 {
		delta = -delta
	}
	if delta > 5*time.Millisecond {
		t.Fatalf("e2e p99 delta warn-off=%s (warn=%s off=%s)", delta, p99Micros(warn), p99Micros(off))
	}
}

func TestI5Memory(t *testing.T) {
	eng, lint := compileEngine(t)
	body := typicalChatBody()
	heap := func() uint64 {
		runtime.GC()
		runtime.GC()
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		return ms.HeapAlloc
	}
	run := func(n int) {
		t.Helper()
		for i := 0; i < n; i++ {
			_ = eng.ValidateRequest(specvalidate.ProfileRelaxed, "openai.chat", body, 0)
			_ = lint.LintRequest("openai.chat", body)
		}
	}
	run(1000)
	mid := heap()
	run(9000)
	end := heap()
	if mid == 0 {
		t.Fatal("mid heap unset")
	}
	limit := mid + mid/10
	if end > limit+64*1024 {
		t.Fatalf("validator heap after 10000=%d mark@1000=%d (10%% band=%d)", end, mid, limit)
	}

	// Also drive 10,000 warn-mode proxy requests, deleting each session so
	// the in-memory trace store is not part of the validator leak budget.
	p := startSpecProxy(t, "warn", "relaxed", chatOK())
	for i := 0; i < 10000; i++ {
		sid := newSession()
		req, err := http.NewRequest(http.MethodPost, p.proxyURL+"/v1/chat/completions", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Test-Session", sid)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("status=%d", res.StatusCode)
		}
		del, err := http.NewRequest(http.MethodDelete, p.controlURL+"/sessions/"+sid, nil)
		if err != nil {
			t.Fatal(err)
		}
		dres, err := http.DefaultClient.Do(del)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, dres.Body)
		_ = dres.Body.Close()
	}
}

func TestG4SSEInterChunkP95(t *testing.T) {
	up := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for i := 0; i < 50; i++ {
			_, _ = fmt.Fprintf(w, "data: {\"id\":\"c\",\"choices\":[{\"delta\":{\"content\":\"%02d\"}}]}\n\n", i)
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	pWarn := startSpecProxy(t, "warn", "relaxed", up)
	pOff := startSpecProxy(t, "off", "relaxed", up)
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	warnP95 := streamP95(t, pWarn, body)
	offP95 := streamP95(t, pOff, body)
	delta := warnP95 - offP95
	if delta < 0 {
		delta = -delta
	}
	if delta > 5*time.Millisecond {
		t.Fatalf("p95 delta=%s warn=%s off=%s (want <=5ms)", delta, warnP95, offP95)
	}
}

func streamP95(t *testing.T, p proxyProc, body []byte) time.Duration {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, p.proxyURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(lab.SessionHeaderName, newSession())
	client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	buf := make([]byte, 64)
	var stamps []time.Time
	for {
		n, err := res.Body.Read(buf)
		if n > 0 {
			stamps = append(stamps, time.Now())
		}
		if err != nil {
			break
		}
	}
	if len(stamps) < 3 {
		t.Fatalf("too few chunks: %d", len(stamps))
	}
	deltas := make([]time.Duration, 0, len(stamps)-1)
	for i := 1; i < len(stamps); i++ {
		deltas = append(deltas, stamps[i].Sub(stamps[i-1]))
	}
	return percentileDuration(deltas, 0.95)
}

func percentileDuration(values []time.Duration, p float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	cp := append([]time.Duration(nil), values...)
	for i := 0; i < len(cp); i++ {
		for j := i + 1; j < len(cp); j++ {
			if cp[j] < cp[i] {
				cp[i], cp[j] = cp[j], cp[i]
			}
		}
	}
	idx := int(float64(len(cp)-1) * p)
	if idx < 0 {
		idx = 0
	}
	if idx >= len(cp) {
		idx = len(cp) - 1
	}
	return cp[idx]
}

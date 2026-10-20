// SPDX-License-Identifier: AGPL-3.0-only

//go:build e2e

package llmentary_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/barzoj-co/llmentary/internal/proxy"
	"github.com/barzoj-co/llmentary/internal/tracetest"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/responses"
)

func TestE2EPathSessionAttribution(t *testing.T) {
	apiKey := requireAPIKey(t)
	model := e2eModel()
	cfg, err := proxy.ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Upstreams = nil
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
	sessionID := fmt.Sprintf("path-%d", time.Now().UnixNano())
	client := openai.NewClient(
		option.WithAPIKey(apiKey),
		option.WithBaseURL(proxySrv.URL+"/s/"+sessionID+"/v1"),
		option.WithMaxRetries(0),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	chatStream := client.Chat.Completions.NewStreaming(ctx, openai.ChatCompletionNewParams{
		Model:       model,
		Temperature: param.NewOpt(0.0),
		MaxTokens:   param.NewOpt(int64(16)),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage("Reply with the single word pong."),
		},
		StreamOptions: openai.ChatCompletionStreamOptionsParam{
			IncludeUsage: openai.Bool(true),
		},
	})
	var sawChat bool
	for chatStream.Next() {
		sawChat = true
	}
	if err := chatStream.Err(); err != nil {
		t.Fatal(err)
	}
	_ = chatStream.Close()
	if !sawChat {
		t.Fatal("expected chat streaming chunks")
	}

	respStream := client.Responses.NewStreaming(ctx, responses.ResponseNewParams{
		Model: model,
		Input: responses.ResponseNewParamsInputUnion{
			OfString: openai.String("Reply with the single word pong."),
		},
		Temperature:     openai.Float(0),
		MaxOutputTokens: openai.Int(16),
	})
	var sawResp bool
	for respStream.Next() {
		sawResp = true
	}
	if err := respStream.Err(); err != nil {
		t.Fatal(err)
	}
	_ = respStream.Close()
	if !sawResp {
		t.Fatal("expected responses streaming events")
	}

	tt, err := tracetest.NewClient(control.URL)
	if err != nil {
		t.Fatal(err)
	}
	calls, err := tt.Wait(ctx, sessionID, 100*time.Millisecond, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Fatalf("calls=%d want 2", len(calls))
	}
	var sawChatCall, sawRespCall bool
	for _, call := range calls {
		if call.SessionID != sessionID || call.SessionSource != "path" {
			t.Fatalf("session=%s source=%s", call.SessionID, call.SessionSource)
		}
		if call.HTTPStatus != 200 {
			t.Fatalf("http_status=%d", call.HTTPStatus)
		}
		if call.Normalized.Usage == nil || call.Normalized.Usage.TotalTokens == nil {
			t.Fatalf("expected usage: %+v", call.Normalized.Usage)
		}
		switch call.APIFamily {
		case "openai.chat":
			sawChatCall = true
		case "openai.responses":
			sawRespCall = true
		}
	}
	if !sawChatCall || !sawRespCall {
		t.Fatalf("missing family: chat=%v responses=%v", sawChatCall, sawRespCall)
	}
}

func TestE2EUnknownPrefixNotForwarded(t *testing.T) {
	_ = requireAPIKey(t)
	cfg, err := proxy.ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Upstreams = nil
	if err := cfg.AddUpstream("openai=https://api.openai.com/v1"); err != nil {
		t.Fatal(err)
	}
	server, err := proxy.NewServer(cfg, proxy.NewMemoryStore(), proxy.TolerantParser{})
	if err != nil {
		t.Fatal(err)
	}
	proxySrv := httptest.NewServer(server.ProxyHandler())
	t.Cleanup(proxySrv.Close)
	before := proxy.UpstreamCallCount.Load()
	req, err := http.NewRequest(http.MethodPost, proxySrv.URL+"/dogfood/v1/chat/completions", bytes.NewReader([]byte(`{"model":"gpt-4.1-mini"}`)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", res.StatusCode, body)
	}
	if !bytes.Contains(body, []byte(`"llmentary_unknown_path"`)) || !bytes.Contains(body, []byte("did you mean /s/dogfood/v1/")) {
		t.Fatalf("body=%s", body)
	}
	if proxy.UpstreamCallCount.Load() != before {
		t.Fatalf("unknown prefix leaked to OpenAI: before=%d after=%d", before, proxy.UpstreamCallCount.Load())
	}
}

func TestE2EGzipAcceptEncodingRoundTrip(t *testing.T) {
	apiKey := requireAPIKey(t)
	model := e2eModel()
	p := startLiveProxy(t, "warn", "relaxed")
	session := liveSession()
	reqBody := []byte(`{"model":` + strconv.Quote(model) + `,"messages":[{"role":"user","content":"Reply with the single word pong."}],"temperature":0,"max_tokens":16}`)
	req, err := http.NewRequest(http.MethodPost, p.proxyURL+"/v1/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set(proxy.SessionHeaderName, session)
	client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.StatusCode, redactLiveBody(raw))
	}
	clientPlain := raw
	if strings.EqualFold(res.Header.Get("Content-Encoding"), "gzip") {
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("client gzip envelope is not valid gzip: %v", err)
		}
		plain, err := io.ReadAll(zr)
		_ = zr.Close()
		if err != nil {
			t.Fatal(err)
		}
		clientPlain = plain
	}
	var chat map[string]any
	if err := json.Unmarshal(clientPlain, &chat); err != nil {
		t.Fatalf("client body is not intact JSON: %v body=%s", err, redactLiveBody(clientPlain))
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
	var sessionJSON map[string]any
	if err := json.Unmarshal(wraw, &sessionJSON); err != nil {
		t.Fatalf("session json: %v", err)
	}
	call := liveLastCall(t, sessionJSON)
	if fmt.Sprint(call["http_status"]) != "200" {
		t.Fatalf("http_status=%v", call["http_status"])
	}
	n := liveNormalized(call)
	usage, _ := n["usage"].(map[string]any)
	if usage == nil || usage["total_tokens"] == nil {
		t.Fatalf("recorded trace did not parse usage: %+v", n)
	}
	if rawResp, _ := call["raw_response"].(string); rawResp == "" {
		t.Fatal("recorded raw_response missing")
	}
}

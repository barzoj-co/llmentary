// SPDX-License-Identifier: AGPL-3.0-only

package proxy_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/barzoj-co/llmentary/internal/proxy"
)

const waitInflightSecret = "SECRET-DOG38-TOKEN-do-not-leak"

func TestWaitTimeoutAwaitingHeaders(t *testing.T) {
	logBuf := &syncLogBuf{}
	p := startSpecProxy(t, "off", "relaxed", stallBeforeHeaders(), func(cfg *proxy.Config) {
		cfg.Logger = slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	})
	sid := newSession()
	cancel, done := startAsyncPOST(t, p.proxyURL+"/v1/responses", sid, []byte(`{"model":"gpt-fixture","input":"hi"}`))
	t.Cleanup(func() {
		cancel()
		<-done
	})
	waitUntilPhase(t, p.controlURL, sid, proxy.InFlightPhaseAwaitingHeaders)
	status, _, body := getJSON(t, p.controlURL+"/sessions/"+sid+"/wait?quiet=20ms&timeout=200ms&min_calls=1")
	if status != http.StatusGatewayTimeout {
		t.Fatalf("status=%d body=%v", status, body)
	}
	item := assertWaitTimeoutInFlight(t, body, 1, 0, 1)
	if fmtString(item["phase"]) != proxy.InFlightPhaseAwaitingHeaders {
		t.Fatalf("phase=%v", item["phase"])
	}
	if item["http_status"] != nil {
		t.Fatalf("http_status=%v", item["http_status"])
	}
	if fmtString(item["path"]) != "/v1/responses" {
		t.Fatalf("path=%v", item["path"])
	}
	if fmtString(item["method"]) != http.MethodPost {
		t.Fatalf("method=%v", item["method"])
	}
	if _, ok := item["started_at"]; !ok {
		t.Fatal("missing started_at")
	}
	assertNoSecrets(t, body, logBuf.String())
	if strings.Contains(logBuf.String(), "proxy upstream headers") {
		t.Fatalf("headers log before headers: %s", logBuf.String())
	}
	assertLiveMatchesVectorKeys(t, body, false)
}

func TestWaitTimeoutStreaming(t *testing.T) {
	logBuf := &syncLogBuf{}
	p := startSpecProxy(t, "off", "relaxed", stallAfterFirstChunk(), func(cfg *proxy.Config) {
		cfg.Logger = slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	})
	sid := newSession()
	cancel, done := startAsyncPOST(t, p.proxyURL+"/v1/responses", sid, []byte(`{"model":"gpt-fixture","input":"hi","stream":true}`))
	t.Cleanup(func() {
		cancel()
		<-done
	})
	waitUntilPhase(t, p.controlURL, sid, proxy.InFlightPhaseStreaming)
	status, _, body := getJSON(t, p.controlURL+"/sessions/"+sid+"/wait?quiet=20ms&timeout=200ms&min_calls=0")
	if status != http.StatusGatewayTimeout {
		t.Fatalf("status=%d body=%v", status, body)
	}
	item := assertWaitTimeoutInFlight(t, body, 1, 0, 0)
	if fmtString(item["phase"]) != proxy.InFlightPhaseStreaming {
		t.Fatalf("phase=%v", item["phase"])
	}
	if item["http_status"] != float64(http.StatusOK) {
		t.Fatalf("http_status=%v", item["http_status"])
	}
	if intFrom(item["bytes_received"]) <= 0 {
		t.Fatalf("bytes_received=%v", item["bytes_received"])
	}
	if fmtString(item["path"]) != "/v1/responses" {
		t.Fatalf("path=%v", item["path"])
	}
	assertNoSecrets(t, body, logBuf.String())
	if !strings.Contains(logBuf.String(), `msg="proxy upstream headers"`) || !strings.Contains(logBuf.String(), "status=200") || !strings.Contains(logBuf.String(), "ttfb_ms=") {
		t.Fatalf("missing headers log: %s", logBuf.String())
	}
	assertLiveMatchesVectorKeys(t, body, true)
}

func TestWaitTimeoutTwoInFlightOrdered(t *testing.T) {
	p := startSpecProxy(t, "off", "relaxed", stallBeforeHeaders())
	sid := newSession()
	other := newSession()
	cancel1, done1 := startAsyncPOST(t, p.proxyURL+"/v1/responses", sid, []byte(`{"model":"gpt-fixture","input":"one"}`))
	cancel2, done2 := startAsyncPOST(t, p.proxyURL+"/v1/responses", sid, []byte(`{"model":"gpt-fixture","input":"two"}`))
	cancelO, doneO := startAsyncPOST(t, p.proxyURL+"/v1/chat/completions", other, []byte(`{"model":"gpt-fixture","messages":[{"role":"user","content":"x"}]}`))
	t.Cleanup(func() {
		cancel1()
		cancel2()
		cancelO()
		<-done1
		<-done2
		<-doneO
	})
	waitUntilInFlightCount(t, p.controlURL, sid, 2)
	waitUntilInFlightCount(t, p.controlURL, other, 1)
	status, _, body := getJSON(t, p.controlURL+"/sessions/"+sid+"/wait?quiet=20ms&timeout=200ms&min_calls=1")
	if status != http.StatusGatewayTimeout {
		t.Fatalf("status=%d body=%v", status, body)
	}
	errObj, _ := body["error"].(map[string]any)
	items, _ := errObj["in_flight"].([]any)
	if len(items) != 2 {
		t.Fatalf("in_flight=%v", errObj["in_flight"])
	}
	first, _ := items[0].(map[string]any)
	second, _ := items[1].(map[string]any)
	if intFrom(first["seq"]) != 1 || intFrom(second["seq"]) != 2 {
		t.Fatalf("seq order %v %v", first["seq"], second["seq"])
	}
	if fmtString(first["path"]) != "/v1/responses" || strings.Contains(fmtString(first["path"]), "chat") {
		t.Fatalf("foreign path leaked: %v", items)
	}
}

func TestWaitTimeoutSettledCallNotInFlight(t *testing.T) {
	var n int
	p := startSpecProxy(t, "off", "relaxed", func(w http.ResponseWriter, r *http.Request) {
		n++
		if n == 1 {
			chatOK()(w, r)
			return
		}
		stallBeforeHeaders()(w, r)
	})
	sid := newSession()
	status, _, _ := postJSON(t, p.proxyURL+"/v1/chat/completions", map[string]string{
		proxy.SessionHeaderName: sid,
	}, []byte(`{"model":"gpt-fixture","messages":[{"role":"user","content":"hi"}]}`))
	if status != http.StatusOK {
		t.Fatalf("first status=%d", status)
	}
	cancel, done := startAsyncPOST(t, p.proxyURL+"/v1/chat/completions", sid, []byte(`{"model":"gpt-fixture","messages":[{"role":"user","content":"two"}]}`))
	t.Cleanup(func() {
		cancel()
		<-done
	})
	waitUntilInFlightCount(t, p.controlURL, sid, 1)
	wstatus, _, body := getJSON(t, p.controlURL+"/sessions/"+sid+"/wait?quiet=20ms&timeout=200ms&min_calls=2")
	if wstatus != http.StatusGatewayTimeout {
		t.Fatalf("status=%d body=%v", wstatus, body)
	}
	item := assertWaitTimeoutInFlight(t, body, 1, 1, 2)
	calls, _ := body["calls"].([]any)
	if len(calls) != 1 {
		t.Fatalf("calls=%v", body["calls"])
	}
	settled, _ := calls[0].(map[string]any)
	if fmtString(settled["id"]) == fmtString(item["call_id"]) {
		t.Fatalf("settled id in in_flight: %v", item)
	}
}

func TestWaitTimeoutQuietPathHasInFlightFields(t *testing.T) {
	p := startSpecProxy(t, "off", "relaxed", chatOK())
	sid := newSession()
	status, _, _ := postJSON(t, p.proxyURL+"/v1/chat/completions", map[string]string{
		proxy.SessionHeaderName: sid,
	}, []byte(`{"model":"gpt-fixture","messages":[{"role":"user","content":"hi"}]}`))
	if status != http.StatusOK {
		t.Fatalf("status=%d", status)
	}
	wstatus, _, body := getJSON(t, p.controlURL+"/sessions/"+sid+"/wait?quiet=5s&timeout=150ms&min_calls=1")
	if wstatus != http.StatusGatewayTimeout {
		t.Fatalf("status=%d body=%v", wstatus, body)
	}
	errObj, _ := body["error"].(map[string]any)
	if intFrom(errObj["active"]) != 0 {
		t.Fatalf("active=%v", errObj["active"])
	}
	if intFrom(errObj["calls_seen"]) != 1 || intFrom(errObj["min_calls"]) != 1 {
		t.Fatalf("error=%v", errObj)
	}
	inFlight, ok := errObj["in_flight"].([]any)
	if !ok || inFlight == nil {
		t.Fatalf("in_flight=%v", errObj["in_flight"])
	}
}

func TestWaitInFlightCancelLeavesAbort(t *testing.T) {
	p := startSpecProxy(t, "off", "relaxed", stallBeforeHeaders())
	sid := newSession()
	cancel, done := startAsyncPOST(t, p.proxyURL+"/v1/responses", sid, []byte(`{"model":"gpt-fixture","input":"hi"}`))
	waitUntilInFlightCount(t, p.controlURL, sid, 1)
	waitDone := make(chan map[string]any, 1)
	waitErr := make(chan error, 1)
	go func() {
		res, err := http.Get(p.controlURL + "/sessions/" + sid + "/wait?quiet=20ms&timeout=3s&min_calls=1")
		if err != nil {
			waitErr <- err
			return
		}
		defer res.Body.Close()
		var body map[string]any
		if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
			waitErr <- err
			return
		}
		waitDone <- body
	}()
	cancel()
	<-done
	var body map[string]any
	select {
	case body = <-waitDone:
	case err := <-waitErr:
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		t.Fatal("wait request did not finish")
	}
	call := lastCall(t, body)
	if call["client_aborted"] != true {
		t.Fatalf("expected abort after cancel: %v", call)
	}
	status, _, got := getJSON(t, p.controlURL+"/sessions/"+sid+"?include=in_flight")
	if status != http.StatusOK {
		t.Fatalf("status=%d", status)
	}
	items, ok := got["in_flight"].([]any)
	if !ok || len(items) != 0 {
		t.Fatalf("in_flight after abort=%v", got["in_flight"])
	}
}

func TestGetSessionIncludeInFlight(t *testing.T) {
	p := startSpecProxy(t, "off", "relaxed", stallBeforeHeaders())
	sid := newSession()
	cancel, done := startAsyncPOST(t, p.proxyURL+"/v1/responses", sid, []byte(`{"model":"gpt-fixture","input":"hi"}`))
	t.Cleanup(func() {
		cancel()
		<-done
	})
	waitUntilInFlightCount(t, p.controlURL, sid, 1)
	status, _, body := getJSON(t, p.controlURL+"/sessions/"+sid)
	if status != http.StatusOK {
		t.Fatalf("status=%d", status)
	}
	if _, ok := body["in_flight"]; ok {
		t.Fatalf("plain GET leaked in_flight: %v", body)
	}
	if calls, _ := body["calls"].([]any); len(calls) != 0 {
		t.Fatalf("plain GET calls=%v", body["calls"])
	}
	status, _, body = getJSON(t, p.controlURL+"/sessions/"+sid+"?include=in_flight")
	if status != http.StatusOK {
		t.Fatalf("include status=%d", status)
	}
	items, _ := body["in_flight"].([]any)
	if len(items) != 1 {
		t.Fatalf("include in_flight=%v", body["in_flight"])
	}
	status, _, body = getJSON(t, p.controlURL+"/sessions/"+sid+"?include=calls")
	if status != http.StatusBadRequest {
		t.Fatalf("bad include status=%d body=%v", status, body)
	}
}

func TestWaitDOG17Unchanged(t *testing.T) {
	p := startSpecProxy(t, "off", "relaxed", chatOK())
	start := time.Now()
	status, _, body := getJSON(t, p.controlURL+"/sessions/ghost-dog17/wait?timeout=3s&quiet=200ms&min_calls=0")
	if status != http.StatusNotFound {
		t.Fatalf("min_calls=0 status=%d body=%v", status, body)
	}
	if time.Since(start) > 400*time.Millisecond {
		t.Fatalf("min_calls=0 took %s", time.Since(start))
	}
	errObj, _ := body["error"].(map[string]any)
	if fmtString(errObj["type"]) != "session_not_found" {
		t.Fatalf("error=%v", errObj)
	}
	start = time.Now()
	status, _, body = getJSON(t, p.controlURL+"/sessions/ghost-dog17b/wait?timeout=200ms&quiet=50ms&min_calls=1")
	if status != http.StatusGatewayTimeout {
		t.Fatalf("min_calls=1 status=%d body=%v", status, body)
	}
	if time.Since(start) < 100*time.Millisecond {
		t.Fatalf("min_calls=1 returned too fast: %s", time.Since(start))
	}
	errObj, _ = body["error"].(map[string]any)
	if fmtString(errObj["type"]) != "wait_timeout" || intFrom(errObj["active"]) != 0 {
		t.Fatalf("error=%v", errObj)
	}
	inFlight, ok := errObj["in_flight"].([]any)
	if !ok || len(inFlight) != 0 {
		t.Fatalf("in_flight=%v", errObj["in_flight"])
	}
}

func stallBeforeHeaders() http.HandlerFunc {
	return func(_ http.ResponseWriter, r *http.Request) {
		holdUntilCancel(r, 10*time.Second)
	}
}

func stallAfterFirstChunk() http.HandlerFunc {
	created := "event: response.created\ndata: {\"type\":\"response.created\",\"sequence_number\":0}\n\n"
	delta := "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"chunk-one\"}\n\n"
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		_, _ = io.WriteString(w, created)
		_, _ = io.WriteString(w, delta)
		if flusher != nil {
			flusher.Flush()
		}
		holdUntilCancel(r, 10*time.Second)
	}
}

func startAsyncPOST(t *testing.T, url, session string, body []byte) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(proxy.SessionHeaderName, session)
	req.Header.Set("Authorization", "Bearer "+waitInflightSecret)
	go func() {
		res, err := http.DefaultClient.Do(req)
		if res != nil {
			_, _ = io.Copy(io.Discard, res.Body)
			_ = res.Body.Close()
		}
		done <- err
	}()
	return cancel, done
}

func waitUntilInFlightCount(t *testing.T, controlURL, session string, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		status, _, body := getJSON(t, controlURL+"/sessions/"+session+"?include=in_flight")
		if status == http.StatusOK {
			if items, _ := body["in_flight"].([]any); len(items) >= n {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("session %s never reached %d in-flight calls", session, n)
}

func waitUntilPhase(t *testing.T, controlURL, session, phase string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var last map[string]any
	var lastStatus int
	for time.Now().Before(deadline) {
		status, _, body := getJSON(t, controlURL+"/sessions/"+session+"?include=in_flight")
		lastStatus, last = status, body
		if status == http.StatusOK {
			if items, _ := body["in_flight"].([]any); len(items) > 0 {
				item, _ := items[0].(map[string]any)
				if fmtString(item["phase"]) == phase {
					return
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("session %s never reached phase %s status=%d body=%v", session, phase, lastStatus, last)
}

func assertWaitTimeoutInFlight(t *testing.T, body map[string]any, active, callsSeen, minCalls int) map[string]any {
	t.Helper()
	errObj, _ := body["error"].(map[string]any)
	if fmtString(errObj["type"]) != "wait_timeout" {
		t.Fatalf("type=%v body=%v", errObj["type"], body)
	}
	if intFrom(errObj["active"]) != active {
		t.Fatalf("active=%v want %d", errObj["active"], active)
	}
	if intFrom(errObj["calls_seen"]) != callsSeen {
		t.Fatalf("calls_seen=%v want %d", errObj["calls_seen"], callsSeen)
	}
	if intFrom(errObj["min_calls"]) != minCalls {
		t.Fatalf("min_calls=%v want %d", errObj["min_calls"], minCalls)
	}
	items, _ := errObj["in_flight"].([]any)
	if len(items) != active {
		t.Fatalf("in_flight len=%d want %d: %v", len(items), active, errObj["in_flight"])
	}
	if active == 0 {
		return nil
	}
	item, _ := items[0].(map[string]any)
	if item == nil {
		t.Fatal("in_flight[0] missing")
	}
	if fmtString(item["call_id"]) == "" {
		t.Fatal("missing call_id")
	}
	if _, ok := item["id"]; ok {
		t.Fatalf("legacy id key present: %v", item["id"])
	}
	return item
}

func TestWaitInFlightRedactsUpstreamUserinfo(t *testing.T) {
	p := startSpecProxy(t, "off", "relaxed", stallBeforeHeaders(), func(cfg *proxy.Config) {
		u := cfg.Upstreams["default"]
		if u == nil {
			t.Fatal("missing default upstream")
		}
		cloned, err := url.Parse(u.String())
		if err != nil {
			t.Fatal(err)
		}
		cloned.User = url.UserPassword("dog38", "hunter2-wait")
		cfg.Upstreams["default"] = cloned
	})
	sid := newSession()
	cancel, done := startAsyncPOST(t, p.proxyURL+"/v1/responses", sid, []byte(`{"model":"gpt-fixture","input":"hi"}`))
	t.Cleanup(func() {
		cancel()
		<-done
	})
	waitUntilInFlightCount(t, p.controlURL, sid, 1)
	status, _, body := getJSON(t, p.controlURL+"/sessions/"+sid+"/wait?quiet=20ms&timeout=200ms&min_calls=1")
	if status != http.StatusGatewayTimeout {
		t.Fatalf("status=%d body=%v", status, body)
	}
	item := assertWaitTimeoutInFlight(t, body, 1, 0, 1)
	raw, _ := json.Marshal(body)
	if strings.Contains(string(raw), "hunter2-wait") || strings.Contains(string(raw), "dog38") {
		t.Fatalf("userinfo leaked: %s", raw)
	}
	if fmtString(item["path"]) != "/v1/responses" {
		t.Fatalf("path=%v", item["path"])
	}
}

func assertNoSecrets(t *testing.T, body map[string]any, log string) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(waitInflightSecret)) || strings.Contains(log, waitInflightSecret) {
		t.Fatalf("secret leaked body=%s log=%s", raw, log)
	}
	if bytes.Contains(raw, []byte("Authorization")) || strings.Contains(strings.ToLower(log), "authorization") {
		t.Fatalf("Authorization leaked body=%s log=%s", raw, log)
	}
	if bytes.Contains(raw, []byte(`"raw_request"`)) || bytes.Contains(raw, []byte(`"raw_response"`)) {
		t.Fatalf("bodies in 504: %s", raw)
	}
}

func assertLiveMatchesVectorKeys(t *testing.T, body map[string]any, streaming bool) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "api", "control", "testdata", "wait_timeout_inflight.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	name := "awaiting_headers"
	if streaming {
		name = "streaming"
	}
	example, _ := doc[name].(map[string]any)
	exErr, _ := example["error"].(map[string]any)
	gotErr, _ := body["error"].(map[string]any)
	for key := range exErr {
		if key == "in_flight" {
			continue
		}
		if _, ok := gotErr[key]; !ok {
			t.Fatalf("live 504 error missing %s (from %s vector)", key, name)
		}
	}
	exItems, _ := exErr["in_flight"].([]any)
	exItem, _ := exItems[0].(map[string]any)
	gotItems, _ := gotErr["in_flight"].([]any)
	gotItem, _ := gotItems[0].(map[string]any)
	for key := range exItem {
		if _, ok := gotItem[key]; !ok {
			t.Fatalf("live in_flight missing %s (from %s vector)", key, name)
		}
	}
}

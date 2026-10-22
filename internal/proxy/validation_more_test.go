// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/barzoj-co/llmentary/internal/schemalint"
	"github.com/barzoj-co/llmentary/internal/specvalidate"
)

func TestGunzipSplitJSONAndMergeValidation(t *testing.T) {
	t.Parallel()
	if _, err := gunzipBytes([]byte("nope"), 16); err == nil {
		t.Fatal("bad gzip")
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write(bytes.Repeat([]byte("x"), 32))
	_ = zw.Close()
	if _, err := gunzipBytes(buf.Bytes(), 8); err == nil {
		t.Fatal("too large")
	}
	var small bytes.Buffer
	zw = gzip.NewWriter(&small)
	_, _ = zw.Write([]byte(`{"ok":true}`))
	_ = zw.Close()
	got, err := gunzipBytes(small.Bytes(), 0)
	if err != nil || string(got) != `{"ok":true}` {
		t.Fatalf("%q %v", got, err)
	}

	first, rest, err := splitFirstJSON([]byte("\uFEFF{\"a\":1}  true"))
	if err != nil || string(first) != `{"a":1}` || !strings.Contains(string(rest), "true") {
		t.Fatalf("first=%q rest=%q err=%v", first, rest, err)
	}
	if _, _, err := splitFirstJSON([]byte(`{`)); err == nil {
		t.Fatal("bad json")
	}

	call := &Call{Normalized: Normalized{Validation: &ValidationInfo{}}}
	mergeResponseValidation(call, []specvalidate.Violation{{Keyword: "type"}}, []schemalint.Finding{{Key: "x"}}, 3, true, 50, SpecTargetOpenAI)
	if !call.Normalized.Validation.ResponseChecked || !call.Normalized.Validation.Truncated {
		t.Fatalf("%+v", call.Normalized.Validation)
	}
	mergeResponseValidation(&Call{}, nil, nil, 0, false, 50, SpecTargetOpenAI)

	warns := requestDecodeWarnings("openai.chat", []byte(`{}`), "application/json")
	if len(warns) == 0 || !strings.Contains(warns[0], "messages") {
		t.Fatalf("%v", warns)
	}
	warns = requestDecodeWarnings("openai.responses", []byte(`{}`), "application/json")
	if len(warns) == 0 || !strings.Contains(warns[0], "model") {
		t.Fatalf("%v", warns)
	}
	warns = requestDecodeWarnings("openai.chat", []byte(`not-json`), "text/plain")
	if len(warns) == 0 {
		t.Fatal("bad json warning")
	}
	if !looksLikeJSON("application/json", nil) || !looksLikeJSON("", []byte(`[]`)) || looksLikeJSON("text/plain", []byte("x")) {
		t.Fatal("looksLikeJSON")
	}
}

func TestWriteEnforceRejectKinds(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	writeEnforceReject(rec, requestValidation{
		reject:     true,
		rejectKind: "spec_violation",
		info:       ValidationInfo{Profile: "strict"},
		violations: []specvalidate.Violation{
			{Side: specvalidate.SideRequest, Pointer: "/model", Path: "model", Message: "missing"},
			{Side: specvalidate.SideRequest, Pointer: "/messages", Path: "messages", Message: "missing"},
			{Side: specvalidate.SideResponse, Pointer: "/id", Message: "skip"},
		},
	})
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "+1 more") || rec.Header().Get("X-Llmentary-Rejected") != "spec_violation" {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	writeEnforceReject(rec, requestValidation{
		reject:     true,
		rejectKind: "ignored_field",
		info:       ValidationInfo{Profile: "relaxed"},
		ignored: []schemalint.Finding{
			{Action: schemalint.ActionReject, Pointer: "/x", Path: "x", Hint: "drop"},
			{Action: schemalint.ActionReject, Pointer: "/y", Path: "y", Hint: "drop"},
			{Action: schemalint.ActionReport, Pointer: "/z", Hint: "ignore"},
		},
	})
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "llmentary_ignored_field") {
		t.Fatalf("%s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	writeEnforceReject(rec, requestValidation{info: ValidationInfo{Profile: "strict"}})
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "llmentary_spec_violation") {
		t.Fatalf("%s", rec.Body.String())
	}
}

func TestSpecModeAndOpenAPIStampCorners(t *testing.T) {
	t.Parallel()
	s := newAbortTestServer(t, NewMemoryStore())
	if s.specMode() == "" {
		t.Fatal("mode")
	}
	if (*Server)(nil).specMode() != DefaultSpecValidation {
		t.Fatal("nil server")
	}
	if string(StampOpenAPIVersion([]byte("openapi: 3.1.0\n"), "1.2.3")) != "openapi: 3.1.0\n" {
		t.Fatal("no info")
	}
	src := []byte("info:\n  title: x\n  version: 0\n")
	got := StampOpenAPIVersion(src, "")
	if string(got) != string(src) {
		t.Fatalf("empty/devel must keep packaged bytes, got %s", got)
	}
	if _, err := OpenAPIInfoVersion([]byte(":\n")); err == nil {
		t.Fatal("bad yaml")
	}
	if _, err := OpenAPIInfoVersion([]byte("info:\n  title: x\n")); err == nil {
		t.Fatal("missing version")
	}
	if err := CheckOpenAPIVersion([]byte("info:\n  version: 1\n"), "2"); err == nil {
		t.Fatal("mismatch")
	}
}

func TestParseChatHelpersAndSSEDelimiter(t *testing.T) {
	t.Parallel()
	if coalesce("", "x") != "x" || coalesce("a", "x") != "a" {
		t.Fatal("coalesce")
	}
	if unionString(nil) != "" || unionString("hi") != "hi" {
		t.Fatal("unionString")
	}
	if unionContentString("hi") != "hi" {
		t.Fatal("unionContent")
	}
	idx, n := sseDelimiter([]byte("a\r\n\r\nb"))
	if idx < 0 || n != 4 {
		t.Fatalf("crlf %d %d", idx, n)
	}
	idx, n = sseDelimiter([]byte("a\n\nb"))
	if idx < 0 || n != 2 {
		t.Fatalf("lf %d %d", idx, n)
	}
	if i, _ := sseDelimiter([]byte("plain")); i != -1 {
		t.Fatal("none")
	}
	if schemaHash(nil) != "" {
		t.Fatal("empty hash")
	}
	if schemaHash(json.RawMessage(`{"b":1,"a":2}`)) == "" {
		t.Fatal("schema hash")
	}
	args, argErr := parseJSONArguments("{")
	if argErr == "" || args != nil {
		t.Fatalf("%s %s", args, argErr)
	}
	if _, argErr := parseJSONArguments(""); argErr != "" {
		t.Fatal("empty args")
	}
	if !hasNestedJSONKey(json.RawMessage(`{"a":{"b":1}}`), "a", "b") {
		t.Fatal("nested")
	}
	if string(jsonField(json.RawMessage(`{"k":"v"}`), "k")) != `"v"` {
		t.Fatal("jsonField")
	}
	if jsonField(json.RawMessage(`{`), "k") != nil {
		t.Fatal("bad jsonField")
	}
}

func TestCapStoredResponseFindingsTargetAware(t *testing.T) {
	t.Parallel()
	overflow := make([]specvalidate.Violation, 0, 6)
	for i := 0; i < 5; i++ {
		overflow = append(overflow, specvalidate.Violation{
			Side: specvalidate.SideResponse, Keyword: specvalidate.KeywordResponseShape,
			Severity: specvalidate.SeverityInfo, Pointer: "/choices/0", Count: 1,
		})
	}
	overflow = append(overflow, specvalidate.Violation{
		Side: specvalidate.SideRequest, Keyword: "type", Pointer: "/max_tokens",
	})

	compat, hit := capStoredResponseFindings(overflow, 2, SpecTargetCompatible)
	if !hit {
		t.Fatal("compatible should truncate")
	}
	var marker specvalidate.Violation
	keptReq := false
	for _, v := range compat {
		if v.Keyword == "truncated" {
			marker = v
		}
		if v.Keyword == "type" && v.Pointer == "/max_tokens" {
			keptReq = true
		}
	}
	if !keptReq {
		t.Fatalf("request finding lost: %v", compat)
	}
	if marker.Keyword != "truncated" || marker.Severity != specvalidate.SeverityInfo || marker.Count != 3 {
		t.Fatalf("compatible marker=%+v want omitted count 3", marker)
	}
	if isWarnSpecFinding(marker) {
		t.Fatal("compatible overflow must not be a warn finding")
	}

	openai, hit := capStoredResponseFindings(overflow, 2, SpecTargetOpenAI)
	if !hit {
		t.Fatal("openai should truncate")
	}
	var oa specvalidate.Violation
	for _, v := range openai {
		if v.Keyword == "truncated" {
			oa = v
		}
	}
	if oa.Keyword != "truncated" || oa.Severity != "" || oa.Count != 3 || !isWarnSpecFinding(oa) {
		t.Fatalf("openai marker must stay a warn finding with omitted count 3: %+v", oa)
	}

	under, hit := capStoredResponseFindings(overflow[:2], 8, SpecTargetCompatible)
	if hit || len(under) != 2 {
		t.Fatalf("under cap hit=%v n=%d", hit, len(under))
	}

	many := make([]specvalidate.Violation, 6)
	for i := range many {
		many[i] = specvalidate.Violation{
			Side: specvalidate.SideResponse, Keyword: specvalidate.KeywordResponseShape,
			Severity: specvalidate.SeverityInfo, Pointer: "/choices/" + string(rune('0'+i)),
		}
	}
	call := &Call{Normalized: Normalized{Validation: &ValidationInfo{Target: SpecTargetCompatible, RequestChecked: true}}}
	mergeResponseValidation(call, many, nil, 1, false, 2, SpecTargetCompatible)
	if !call.Normalized.Validation.Truncated {
		t.Fatal("merge should mark truncated")
	}
	if specLogValue(*call, "warn") != "clean" {
		t.Fatalf("compatible overflow log=%q", specLogValue(*call, "warn"))
	}
	if hasWarnSpecFindings(*call) {
		t.Fatalf("compatible overflow must stay out of session findings: %v", *call.Normalized.SpecViolations)
	}

	oaCall := &Call{Normalized: Normalized{Validation: &ValidationInfo{Target: SpecTargetOpenAI, RequestChecked: true}}}
	raw := []specvalidate.Violation{
		{Side: specvalidate.SideResponse, Keyword: "enum", Pointer: "/a"},
		{Side: specvalidate.SideResponse, Keyword: "enum", Pointer: "/b"},
		{Side: specvalidate.SideResponse, Keyword: "enum", Pointer: "/c"},
	}
	mergeResponseValidation(oaCall, raw, nil, 1, false, 1, SpecTargetOpenAI)
	if got := specLogValue(*oaCall, "warn"); got != "findings:3" {
		t.Fatalf("openai overflow log=%q want findings:3", got)
	}

	grouped := []specvalidate.Violation{
		{Side: specvalidate.SideResponse, Keyword: "enum", Pointer: "/a", Count: 3},
		{Side: specvalidate.SideResponse, Keyword: "enum", Pointer: "/b", Count: 2},
	}
	groupedOut, hit := capStoredResponseFindings(grouped, 1, SpecTargetOpenAI)
	if !hit {
		t.Fatal("grouped should truncate")
	}
	var groupedMarker specvalidate.Violation
	for _, v := range groupedOut {
		if v.Keyword == "truncated" {
			groupedMarker = v
		}
	}
	if groupedMarker.Count != 2 {
		t.Fatalf("grouped omitted count=%d want 2: %+v", groupedMarker.Count, groupedOut)
	}
	groupedCall := Call{Normalized: Normalized{Validation: &ValidationInfo{RequestChecked: true, ResponseChecked: true}, SpecViolations: &groupedOut}}
	if got := specLogValue(groupedCall, "warn"); got != "findings:5" {
		t.Fatalf("grouped openai log=%q want findings:5", got)
	}
}

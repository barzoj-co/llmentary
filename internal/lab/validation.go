// SPDX-License-Identifier: AGPL-3.0-only

package lab

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/barzoj-co/llm-test-lab/internal/schemalint"
	"github.com/barzoj-co/llm-test-lab/internal/specvalidate"
)

type validationRuntime struct {
	mode     string
	profile  string
	maxBytes int
	engine   *specvalidate.Engine
	linter   *schemalint.Linter
}

type requestValidation struct {
	info       ValidationInfo
	violations []specvalidate.Violation
	ignored    []schemalint.Finding
	warnings   []string
	reject     bool
	rejectKind string
}

func newValidationRuntime(cfg Config) (*validationRuntime, error) {
	rt := &validationRuntime{
		mode:     cfg.SpecValidation,
		profile:  cfg.SpecProfile,
		maxBytes: cfg.SpecValidationMaxBytes,
	}
	if rt.mode == "" {
		rt.mode = DefaultSpecValidation
	}
	if rt.profile == "" {
		rt.profile = DefaultSpecProfile
	}
	if rt.maxBytes == 0 {
		rt.maxBytes = DefaultSpecValidationMaxBytes
	}
	if rt.mode == specvalidate.ModeOff {
		return rt, nil
	}
	eng, err := specvalidate.CompileEmbedded()
	if err != nil {
		return nil, fmt.Errorf("compile spec validation profiles: %w", err)
	}
	subset, err := schemalint.LoadSubset(specvalidate.EmbeddedSchemaSubset)
	if err != nil {
		return nil, fmt.Errorf("load schema-subset.yaml: %w", err)
	}
	rt.engine = eng
	rt.linter = schemalint.New(subset)
	if cfg.Logger != nil {
		cfg.Logger.Debug("spec validation compiled",
			"profiles", "strict,relaxed",
			"duration_ns", specvalidate.LastCompileDurationNS.Load(),
		)
	}
	return rt, nil
}

func (rt *validationRuntime) configJSON() map[string]any {
	sha := ""
	retrieved := ""
	if rt != nil && rt.engine != nil {
		sha = rt.engine.SpecSHA
		retrieved = rt.engine.SubsetRetrieved
	}
	mode := DefaultSpecValidation
	profile := DefaultSpecProfile
	if rt != nil {
		mode = rt.mode
		profile = rt.profile
	}
	return map[string]any{
		"spec_validation":         mode,
		"spec_profile":            profile,
		"spec_sha":                sha,
		"schema_subset_retrieved": retrieved,
	}
}

func (rt *validationRuntime) checkRequest(apiFamily, contentType, contentEncoding string, raw []byte) requestValidation {
	var out requestValidation
	out.warnings = requestDecodeWarnings(apiFamily, raw, contentType)
	if rt == nil || rt.mode == specvalidate.ModeOff {
		return out
	}
	out.info = ValidationInfo{
		Mode:                  rt.mode,
		Profile:               rt.profile,
		SpecSHA:               rt.engine.SpecSHA,
		SchemaSubsetRetrieved: rt.engine.SubsetRetrieved,
		RequestChecked:        true,
		ResponseChecked:       false,
	}
	validateBytes := raw
	if strings.EqualFold(contentEncoding, "gzip") {
		plain, err := gunzipBytes(raw, rt.maxBytes)
		if err != nil {
			if errors.Is(err, errDecompressedTooLarge) {
				out.info.SkippedReason = strPtr("body_too_large")
				out.info.RequestChecked = false
				out.warnings = append(out.warnings, "spec validation skipped: body_too_large")
				return out
			}
			out.info.SkippedReason = strPtr("unsupported_content_encoding")
			out.info.RequestChecked = false
			out.warnings = append(out.warnings, "spec validation skipped: gzip body could not be decompressed")
			return out
		}
		validateBytes = plain
	} else if contentEncoding != "" && !strings.EqualFold(contentEncoding, "identity") {
		out.info.SkippedReason = strPtr("unsupported_content_encoding")
		out.info.RequestChecked = false
		out.warnings = append(out.warnings, "spec validation skipped: unsupported Content-Encoding")
		return out
	}
	if !looksLikeJSON(contentType, validateBytes) {
		out.info.SkippedReason = strPtr("non_json_content_type")
		out.info.RequestChecked = false
		out.warnings = append(out.warnings, "spec validation skipped: non-JSON content type")
		return out
	}
	if contentType != "" && !strings.Contains(strings.ToLower(contentType), "json") && looksLikeJSON(contentType, validateBytes) {
		out.warnings = append(out.warnings, "content-type is "+contentType+" but body is JSON; validated as JSON")
	}
	schemaRes := rt.engine.ValidateRequest(rt.profile, apiFamily, validateBytes, rt.maxBytes)
	out.info.DurationUS += schemaRes.DurationUS
	out.info.Truncated = schemaRes.Truncated
	if schemaRes.SkippedReason != "" {
		out.info.SkippedReason = strPtr(schemaRes.SkippedReason)
		out.info.RequestChecked = false
		out.warnings = append(out.warnings, "spec validation skipped: "+schemaRes.SkippedReason)
		return out
	}
	out.violations = append(out.violations, schemaRes.Violations...)
	lintRes := rt.linter.LintRequest(apiFamily, validateBytes)
	out.ignored = append(out.ignored, lintRes.Ignored...)
	if n := len(lintRes.Ignored); n > 0 && lintRes.Ignored[n-1].Kind == "truncated" {
		out.info.Truncated = true
	}
	for i := range lintRes.StrictViolations {
		lintRes.StrictViolations[i].Profile = rt.profile
	}
	out.violations = append(out.violations, lintRes.StrictViolations...)
	out.reject, out.rejectKind = shouldReject(out.violations, out.ignored)
	return out
}

func (rt *validationRuntime) checkResponse(apiFamily string, status int, raw []byte, stream bool, events []SSEEvent) (violations []specvalidate.Violation, ignored []schemalint.Finding, duration int64, truncated bool) {
	if rt == nil || rt.mode == specvalidate.ModeOff || rt.engine == nil {
		return nil, nil, 0, false
	}
	if status >= 400 {
		return nil, nil, 0, false
	}
	if stream && len(events) > 0 {
		for i, ev := range events {
			if len(bytes.TrimSpace(ev.Data)) == 0 {
				continue
			}
			idx := i
			res := rt.engine.ValidateResponse(rt.profile, apiFamily, ev.Data, true, &idx)
			violations = append(violations, res.Violations...)
			duration += res.DurationUS
			truncated = truncated || res.Truncated
		}
		return violations, ignored, duration, truncated
	}
	res := rt.engine.ValidateResponse(rt.profile, apiFamily, raw, false, nil)
	return res.Violations, nil, res.DurationUS, res.Truncated
}

func shouldReject(violations []specvalidate.Violation, ignored []schemalint.Finding) (bool, string) {
	for _, v := range violations {
		if v.Side == specvalidate.SideRequest || v.Side == "" {
			return true, "spec_violation"
		}
	}
	for _, f := range ignored {
		if f.Action == schemalint.ActionReject && f.Severity != schemalint.SeverityInfo {
			return true, "ignored_field"
		}
	}
	return false, ""
}

func applyRequestValidation(call *Call, rv requestValidation) {
	if rv.info.Mode == "" || rv.info.Mode == specvalidate.ModeOff {
		call.Normalized.ParserWarnings = append(call.Normalized.ParserWarnings, rv.warnings...)
		return
	}
	info := rv.info
	call.Normalized.Validation = &info
	vs := make([]specvalidate.Violation, 0, len(rv.violations))
	vs = append(vs, rv.violations...)
	ig := make([]schemalint.Finding, 0, len(rv.ignored))
	ig = append(ig, rv.ignored...)
	call.Normalized.SpecViolations = &vs
	call.Normalized.IgnoredFields = &ig
	call.Normalized.ParserWarnings = append(call.Normalized.ParserWarnings, rv.warnings...)
}

func mergeResponseValidation(call *Call, violations []specvalidate.Violation, extraIgnored []schemalint.Finding, duration int64, truncated bool) {
	if call.Normalized.Validation == nil {
		return
	}
	call.Normalized.Validation.ResponseChecked = true
	call.Normalized.Validation.DurationUS += duration
	if truncated {
		call.Normalized.Validation.Truncated = true
	}
	if call.Normalized.SpecViolations == nil {
		empty := []specvalidate.Violation{}
		call.Normalized.SpecViolations = &empty
	}
	*call.Normalized.SpecViolations = append(*call.Normalized.SpecViolations, violations...)
	if call.Normalized.IgnoredFields == nil {
		empty := []schemalint.Finding{}
		call.Normalized.IgnoredFields = &empty
	}
	*call.Normalized.IgnoredFields = append(*call.Normalized.IgnoredFields, extraIgnored...)
}

func writeEnforceReject(w http.ResponseWriter, rv requestValidation) {
	kind := rv.rejectKind
	if kind == "" {
		kind = "spec_violation"
	}
	code := "llmtestlab_spec_violation"
	if kind == "ignored_field" {
		code = "llmtestlab_ignored_field"
	}
	param := ""
	first := ""
	more := 0
	if kind == "spec_violation" {
		for _, v := range rv.violations {
			if v.Side == specvalidate.SideResponse {
				continue
			}
			if first == "" {
				first = v.Pointer + ": " + v.Message
				param = v.Path
			} else {
				more++
			}
		}
	} else {
		for _, f := range rv.ignored {
			if f.Action != schemalint.ActionReject {
				continue
			}
			if first == "" {
				first = f.Pointer + ": " + f.Hint
				param = f.Path
			} else {
				more++
			}
		}
	}
	msg := fmt.Sprintf("llm-test-lab: request rejected (profile=%s): %s", rv.info.Profile, first)
	if more > 0 {
		msg += fmt.Sprintf(" (+%d more)", more)
	}
	body := map[string]any{
		"error": map[string]any{
			"message": msg,
			"type":    "invalid_request_error",
			"param":   param,
			"code":    code,
		},
	}
	raw, _ := json.Marshal(body)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-LLMTestLab-Rejected", kind)
	w.WriteHeader(http.StatusBadRequest)
	_, _ = w.Write(raw)
}

func requestDecodeWarnings(apiFamily string, raw []byte, contentType string) []string {
	var out []string
	first, rest, err := splitFirstJSON(raw)
	if err != nil {
		if len(bytes.TrimSpace(raw)) > 0 {
			out = append(out, "request does not match spec: "+err.Error())
		}
		return out
	}
	if i := indexNonWS(rest); i >= 0 {
		offset := len(raw) - len(rest) + i
		if bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}) {
			offset += 0
		}
		out = append(out, fmt.Sprintf("trailing_data_after_json at offset %d", offset))
	}
	if bytes.Equal(bytes.TrimSpace(first), []byte("{}")) {
		switch apiFamily {
		case "openai.chat":
			out = append(out, warnMissingRequired+": messages")
		case "openai.responses":
			out = append(out, warnMissingRequired+": model")
		}
	}
	_ = contentType
	return out
}

func looksLikeJSON(contentType string, raw []byte) bool {
	trim := bytes.TrimSpace(trimBOM(raw))
	if len(trim) == 0 {
		return strings.Contains(strings.ToLower(contentType), "json")
	}
	if trim[0] == '{' || trim[0] == '[' {
		return true
	}
	return strings.Contains(strings.ToLower(contentType), "json")
}

var errDecompressedTooLarge = errors.New("decompressed body exceeds spec-validation-max-bytes")

func gunzipBytes(raw []byte, maxBytes int) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	if maxBytes <= 0 {
		maxBytes = specvalidate.DefaultMaxBytes
	}
	limited, err := io.ReadAll(io.LimitReader(r, int64(maxBytes)+1))
	if err != nil {
		return nil, err
	}
	if len(limited) > maxBytes {
		return nil, errDecompressedTooLarge
	}
	return limited, nil
}

func strPtr(s string) *string { return &s }

func splitFirstJSON(raw []byte) (first, rest []byte, err error) {
	raw = trimBOM(raw)
	dec := json.NewDecoder(bytes.NewReader(raw))
	var v json.RawMessage
	if err := dec.Decode(&v); err != nil {
		return nil, raw, err
	}
	off := int(dec.InputOffset())
	if off < 0 {
		off = 0
	}
	if off > len(raw) {
		off = len(raw)
	}
	return v, raw[off:], nil
}

func trimBOM(raw []byte) []byte {
	if bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}) {
		return raw[3:]
	}
	return raw
}

func indexNonWS(b []byte) int {
	for i, c := range b {
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			return i
		}
	}
	return -1
}

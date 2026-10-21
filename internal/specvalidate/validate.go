// SPDX-License-Identifier: AGPL-3.0-only

package specvalidate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func (e *Engine) ValidateRequest(profile, apiFamily string, raw []byte, maxBytes int) Result {
	start := time.Now()
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	res := Result{Checked: true}
	if len(raw) > maxBytes {
		res.SkippedReason = "body_too_large"
		res.Checked = false
		res.DurationUS = time.Since(start).Microseconds()
		return res
	}
	skip := map[string]bool{}
	if profile == ProfileRelaxed {
		for _, s := range e.overlay.Prechecks.Skip {
			skip[s.ID] = true
		}
	}
	res.Violations = append(res.Violations, prechecks(raw, profile, precheckConfig{skip: skip})...)
	if firstJSONObject(raw) {
		if sch := e.requestSchema(profile, apiFamily); sch != nil {
			res.Violations = append(res.Violations, e.schemaViolations(sch, raw, profile, SideRequest, apiFamily, nil)...)
		}
	}
	res.Violations = capAndSort(res.Violations, &res.Truncated, SideRequest)
	res.DurationUS = time.Since(start).Microseconds()
	return res
}

func (e *Engine) ValidateResponse(profile, apiFamily string, raw []byte, stream bool, eventIndex *int) Result {
	start := time.Now()
	res := Result{Checked: true}
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("[DONE]")) {
		res.DurationUS = time.Since(start).Microseconds()
		return res
	}
	sch := e.responseSchema(profile, apiFamily, stream)
	if sch != nil {
		res.Violations = append(res.Violations, e.schemaViolations(sch, raw, profile, SideResponse, apiFamily, eventIndex)...)
	}
	res.Violations = capAndSort(res.Violations, &res.Truncated, SideResponse)
	res.DurationUS = time.Since(start).Microseconds()
	return res
}

func (e *Engine) requestSchema(profile, apiFamily string) *jsonschema.Schema {
	if profile == ProfileRelaxed {
		if apiFamily == "openai.responses" {
			return e.relaxedRespReq
		}
		return e.relaxedChatReq
	}
	if apiFamily == "openai.responses" {
		return e.strictRespReq
	}
	return e.strictChatReq
}

func (e *Engine) responseSchema(profile, apiFamily string, stream bool) *jsonschema.Schema {
	relaxed := profile == ProfileRelaxed
	if apiFamily == "openai.responses" {
		if stream {
			if relaxed {
				return e.relaxedRespSSE
			}
			return e.strictRespSSE
		}
		if relaxed {
			return e.relaxedRespResp
		}
		return e.strictRespResp
	}
	if stream {
		if relaxed {
			return e.relaxedChatSSE
		}
		return e.strictChatSSE
	}
	if relaxed {
		return e.relaxedChatResp
	}
	return e.strictChatResp
}

func (e *Engine) schemaViolations(sch *jsonschema.Schema, raw []byte, profile, side, apiFamily string, eventIndex *int) []Violation {
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(trimBOM(firstJSONBytes(raw))))
	if err != nil {
		return nil
	}
	err = sch.Validate(inst)
	if err == nil {
		return nil
	}
	var out []Violation
	for _, item := range flattenSchemaError(err) {
		pointer := item.pointer
		if pointer == "" {
			pointer = "/"
		}
		out = append(out, Violation{
			Side:       side,
			Source:     SourceSchema,
			Path:       PointerToPath(pointer),
			Pointer:    pointer,
			Keyword:    item.keyword,
			Message:    unknownFieldMessage(item, side, apiFamily),
			Profile:    profile,
			SchemaRef:  item.schemaRef,
			EventIndex: eventIndex,
		})
	}
	return out
}

func unknownFieldMessage(item schemaErr, side, apiFamily string) string {
	if item.keyword != "unevaluatedProperties" && item.keyword != "additionalProperties" {
		return item.message
	}
	field := lastPointerField(item.pointer)
	if field == "" {
		return item.message
	}
	return fmt.Sprintf("unknown field %q (not in the OpenAI spec for %s %s)", field, specDisplayName(apiFamily), side)
}

func specDisplayName(apiFamily string) string {
	if apiFamily == "openai.responses" {
		return "responses"
	}
	return "chat.completions"
}

func lastPointerField(pointer string) string {
	pointer = strings.Trim(pointer, "/")
	if pointer == "" {
		return ""
	}
	parts := strings.Split(pointer, "/")
	part := strings.ReplaceAll(strings.ReplaceAll(parts[len(parts)-1], "~1", "/"), "~0", "~")
	return part
}

type schemaErr struct {
	pointer   string
	keyword   string
	message   string
	schemaRef string
}

func flattenSchemaError(err error) []schemaErr {
	ve, ok := err.(*jsonschema.ValidationError)
	if !ok {
		return []schemaErr{{keyword: "schema", message: err.Error()}}
	}
	var out []schemaErr
	var walk func(*jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if e == nil {
			return
		}
		if len(e.Causes) > 0 {
			for _, c := range e.Causes {
				walk(c)
			}
			return
		}
		msg := e.Error()
		if i := strings.LastIndex(msg, ": "); i >= 0 {
			msg = msg[i+2:]
		}
		kw := ""
		if e.ErrorKind != nil {
			if p := e.ErrorKind.KeywordPath(); len(p) > 0 {
				kw = p[len(p)-1]
			}
		}
		if kw == "" {
			kw = keywordFromLocation("", e.SchemaURL, msg)
		}
		item := schemaErr{
			pointer:   instancePointer(e.InstanceLocation),
			keyword:   kw,
			message:   msg,
			schemaRef: e.SchemaURL,
		}
		if isWrapperSchemaErr(item) || isCompositionArtifact(item) {
			return
		}
		out = append(out, item)
	}
	walk(ve)
	return dropInputTypeIfNested(out)
}

func dropInputTypeIfNested(in []schemaErr) []schemaErr {
	// InputParam is oneOf[string, array]. The losing branch emits a type
	// error at /input ("got array, want string" or the reverse). Drop that
	// artifact, and also drop /input type when a real nested /input/ error
	// exists. A genuine mismatch (e.g. input:123) is kept.
	hasNested := false
	losingBranch := false
	for _, item := range in {
		if strings.HasPrefix(item.pointer, "/input/") {
			hasNested = true
		}
		if item.keyword == "type" && (item.pointer == "/input" || item.pointer == "input") {
			msg := strings.ToLower(item.message)
			if strings.Contains(msg, "got array, want string") || strings.Contains(msg, "got string, want array") {
				losingBranch = true
			}
		}
	}
	if !hasNested && !losingBranch {
		return in
	}
	out := make([]schemaErr, 0, len(in))
	for _, item := range in {
		if item.keyword == "type" && (item.pointer == "/input" || item.pointer == "input") {
			continue
		}
		out = append(out, item)
	}
	return out
}

func isCompositionArtifact(item schemaErr) bool {
	// unevaluatedProperties on the allOf composer can flag known properties
	// when a nested oneOf fails and annotations are not collected.
	if item.keyword == "unevaluatedProperties" {
		switch item.pointer {
		case "/model", "/messages", "/input", "/tools", "/temperature", "/stream":
			return true
		}
	}
	return false
}

func isWrapperSchemaErr(item schemaErr) bool {
	switch item.keyword {
	case "allOf", "anyOf", "oneOf", "schema":
		msg := strings.ToLower(item.message)
		return strings.Contains(msg, "failed") || msg == ""
	}
	return false
}

func keywordFromLocation(loc, abs, msg string) string {
	for _, key := range []string{"unevaluatedProperties", "additionalProperties", "required", "type", "enum", "const"} {
		if strings.Contains(loc, key) || strings.Contains(abs, key) || strings.Contains(strings.ToLower(msg), strings.ToLower(key)) {
			return key
		}
	}
	if i := strings.LastIndex(loc, "/"); i >= 0 {
		k := loc[i+1:]
		if k != "" && !strings.ContainsAny(k, "~0123456789") {
			return k
		}
	}
	return "schema"
}

func instancePointer(loc any) string {
	switch v := loc.(type) {
	case string:
		return v
	case []string:
		if len(v) == 0 {
			return ""
		}
		var b strings.Builder
		for _, p := range v {
			b.WriteByte('/')
			b.WriteString(strings.ReplaceAll(strings.ReplaceAll(p, "~", "~0"), "/", "~1"))
		}
		return b.String()
	default:
		return fmt.Sprint(loc)
	}
}

func firstJSONObject(raw []byte) bool {
	raw = bytes.TrimSpace(trimBOM(raw))
	return len(raw) > 0 && raw[0] == '{'
}

func firstJSONBytes(raw []byte) []byte {
	raw = trimBOM(raw)
	dec := json.NewDecoder(bytes.NewReader(raw))
	var v json.RawMessage
	if err := dec.Decode(&v); err != nil {
		return raw
	}
	return v
}

func trimBOM(raw []byte) []byte {
	if bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}) {
		return raw[3:]
	}
	return raw
}

func capAndSort(in []Violation, truncated *bool, side string) []Violation {
	sort.SliceStable(in, func(i, j int) bool {
		if in[i].Side != in[j].Side {
			return in[i].Side < in[j].Side
		}
		if in[i].Pointer != in[j].Pointer {
			return in[i].Pointer < in[j].Pointer
		}
		return in[i].Keyword < in[j].Keyword
	})
	if len(in) <= MaxFindings {
		return in
	}
	kept := append([]Violation(nil), in[:MaxFindings]...)
	kept = append(kept, Violation{
		Side:    side,
		Source:  SourceSyntax,
		Keyword: "truncated",
		Message: "too many findings",
		Count:   len(in),
	})
	*truncated = true
	return kept
}

// CollapseDuplicates keeps one finding per (side, keyword, pointer, message)
// and sets Count to the number of copies. The first event_index is kept.
// Streamed per-chunk copies therefore become one stored row with count=N.
func CollapseDuplicates(in []Violation) []Violation {
	if len(in) <= 1 {
		return in
	}
	type key struct {
		side, keyword, pointer, message string
	}
	seen := map[key]int{}
	out := make([]Violation, 0, len(in))
	for _, v := range in {
		k := key{v.Side, v.Keyword, v.Pointer, v.Message}
		if i, ok := seen[k]; ok {
			if out[i].Count < 1 {
				out[i].Count = 1
			}
			out[i].Count++
			continue
		}
		seen[k] = len(out)
		cp := v
		if cp.Count < 1 {
			cp.Count = 1
		}
		out = append(out, cp)
	}
	return out
}

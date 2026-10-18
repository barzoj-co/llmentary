// SPDX-License-Identifier: AGPL-3.0-only

package schemalint

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/barzoj-co/llm-test-lab/internal/specvalidate"
)

type Result struct {
	Ignored          []Finding
	StrictViolations []specvalidate.Violation
}

func (l *Linter) LintRequest(apiFamily string, raw []byte) Result {
	var body map[string]any
	if json.Unmarshal(firstObject(raw), &body) != nil {
		return Result{}
	}
	model, _ := body["model"].(string)
	fineTuned := strings.HasPrefix(model, "ft:")
	var res Result
	switch apiFamily {
	case "openai.chat":
		res.merge(l.lintChat(body, fineTuned))
	case "openai.responses":
		res.merge(l.lintResponses(body, fineTuned))
	}
	res.Ignored = capFindings(sortFindings(res.Ignored))
	res.StrictViolations = specvalidateCap(res.StrictViolations)
	return res
}

func (l *Linter) ConfirmStrictFallback(rawResponse []byte, requestOmitted []int) []Finding {
	var body map[string]any
	if json.Unmarshal(firstObject(rawResponse), &body) != nil {
		return nil
	}
	tools, _ := body["tools"].([]any)
	var out []Finding
	for _, idx := range requestOmitted {
		if idx < 0 || idx >= len(tools) {
			continue
		}
		tool, _ := tools[idx].(map[string]any)
		if tool == nil {
			continue
		}
		if b, ok := tool["strict"].(bool); ok && !b {
			ptr := "/tools/" + strconv.Itoa(idx) + "/strict"
			out = append(out, Finding{
				Path: specvalidate.PointerToPath(ptr), Pointer: ptr,
				Key: "strict", Kind: KindStrictFallback, Context: ContextResponsesTools,
				EffectiveStrict: false, UpstreamBehavior: BehaviorFallback,
				Evidence: "live:TestLiveIgnored_EnumDescriptions_ResponsesToolStrictOmitted",
				Severity: SeverityWarning, Action: ActionReject,
				Hint: l.Subset.Hint("strict_fallback"), Confirmed: true,
			})
		}
	}
	return out
}

func (l *Linter) lintChat(body map[string]any, fineTuned bool) Result {
	var res Result
	if msgs, ok := body["messages"].([]any); ok {
		for i, item := range msgs {
			msg, _ := item.(map[string]any)
			if msg == nil {
				continue
			}
			base := "/messages/" + strconv.Itoa(i)
			res.Ignored = append(res.Ignored, unknownKeys(msg, chatMessageKeys, base, ContextChatMessage, l)...)
			if parts, ok := msg["content"].([]any); ok {
				for j, part := range parts {
					p, _ := part.(map[string]any)
					res.Ignored = append(res.Ignored, unknownKeys(p, chatContentPartKeys, base+"/content/"+strconv.Itoa(j), ContextChatMessage, l)...)
				}
			}
			if calls, ok := msg["tool_calls"].([]any); ok {
				for j, call := range calls {
					c, _ := call.(map[string]any)
					res.Ignored = append(res.Ignored, unknownKeys(c, chatToolCallKeys, base+"/tool_calls/"+strconv.Itoa(j), ContextChatMessage, l)...)
				}
			}
		}
	}
	if tools, ok := body["tools"].([]any); ok {
		for i, item := range tools {
			tool, _ := item.(map[string]any)
			if tool == nil {
				continue
			}
			fn, _ := tool["function"].(map[string]any)
			if fn == nil {
				continue
			}
			base := fmt.Sprintf("/tools/%d/function", i)
			res.Ignored = append(res.Ignored, unknownKeys(fn, chatFunctionKeys, base, ContextChatToolFunction, l)...)
			strict, hasStrict := boolVal(fn["strict"])
			effective := hasStrict && strict
			if params := fn["parameters"]; params != nil {
				res.merge(l.walkUserSchema(params, base+"/parameters", ContextChatToolParams, effective, fineTuned, hasStrict, false))
			}
		}
	}
	if fns, ok := body["functions"].([]any); ok {
		for i, item := range fns {
			fn, _ := item.(map[string]any)
			if fn == nil {
				continue
			}
			base := fmt.Sprintf("/functions/%d", i)
			if params := fn["parameters"]; params != nil {
				res.merge(l.walkUserSchema(params, base+"/parameters", ContextChatFunctionsParams, false, fineTuned, false, false))
			}
		}
	}
	if rf, ok := body["response_format"].(map[string]any); ok {
		if js, ok := rf["json_schema"].(map[string]any); ok {
			strict, hasStrict := boolVal(js["strict"])
			if schema := js["schema"]; schema != nil {
				res.merge(l.walkUserSchema(schema, "/response_format/json_schema/schema", ContextChatJSONSchema, hasStrict && strict, fineTuned, hasStrict, false))
			}
		}
	}
	return res
}

func (l *Linter) lintResponses(body map[string]any, fineTuned bool) Result {
	var res Result
	if tools, ok := body["tools"].([]any); ok {
		for i, item := range tools {
			tool, _ := item.(map[string]any)
			if tool == nil {
				continue
			}
			typ, _ := tool["type"].(string)
			if typ != "" && typ != "function" {
				continue
			}
			base := fmt.Sprintf("/tools/%d", i)
			res.Ignored = append(res.Ignored, unknownKeys(tool, responsesFunctionKeys, base, ContextResponsesTools, l)...)
			strict, hasStrict := boolVal(tool["strict"])
			effective := hasStrict && strict
			if params := tool["parameters"]; params != nil {
				res.merge(l.walkUserSchema(params, base+"/parameters", ContextResponsesToolParams, effective, fineTuned, hasStrict, true))
			}
		}
	}
	if text, ok := body["text"].(map[string]any); ok {
		if format, ok := text["format"].(map[string]any); ok {
			if typ, _ := format["type"].(string); typ == "json_schema" || format["schema"] != nil {
				strict, hasStrict := boolVal(format["strict"])
				if schema := format["schema"]; schema != nil {
					res.merge(l.walkUserSchema(schema, "/text/format/schema", ContextResponsesTextFormat, hasStrict && strict, fineTuned, hasStrict, false))
				}
			}
		}
	}
	if input, ok := body["input"].([]any); ok {
		for i, item := range input {
			obj, _ := item.(map[string]any)
			if obj == nil {
				continue
			}
			base := fmt.Sprintf("/input/%d", i)
			if content, ok := obj["content"].([]any); ok {
				for j, part := range content {
					p, _ := part.(map[string]any)
					res.Ignored = append(res.Ignored, unknownKeys(p, responsesContentPartKeys, base+"/content/"+strconv.Itoa(j), ContextResponsesTools, l)...)
				}
			}
		}
	}
	return res
}

func (l *Linter) walkUserSchema(schema any, pointer, context string, effectiveStrict, fineTuned, hasStrict, responses bool) Result {
	var res Result
	obj, _ := schema.(map[string]any)
	if obj == nil {
		return res
	}
	if effectiveStrict {
		if _, isAnyOf := obj["anyOf"]; isAnyOf {
			res.StrictViolations = append(res.StrictViolations, specvalidate.Violation{
				Side: specvalidate.SideRequest, Source: specvalidate.SourceStrictMode,
				Path: specvalidate.PointerToPath(pointer), Pointer: pointer,
				Keyword: "anyOf", Message: "strict mode root must be an object, not anyOf",
			})
		}
		if typ, _ := obj["type"].(string); typ != "" && typ != "object" {
			res.StrictViolations = append(res.StrictViolations, specvalidate.Violation{
				Side: specvalidate.SideRequest, Source: specvalidate.SourceStrictMode,
				Path: specvalidate.PointerToPath(pointer), Pointer: pointer,
				Keyword: "type", Message: "strict mode root must be an object",
			})
		}
	}
	if !effectiveStrict && responses && !hasStrict && !strictCompatible(obj) {
		res.Ignored = append(res.Ignored, Finding{
			Path: specvalidate.PointerToPath(pointer), Pointer: pointer,
			Key: "strict", Kind: KindStrictFallbackPred, Context: context,
			EffectiveStrict: false, UpstreamBehavior: BehaviorFallback,
			Evidence: "live:TestLiveSpec_FunctionToolWithoutStrict_Responses",
			Severity: SeverityWarning, Action: ActionReject,
			Hint: l.Subset.Hint("strict_fallback_predicted"),
		})
	}

	defs := mergeDefs(nil, obj)
	findings := l.lintSchemaRoot(obj, pointer, specvalidate.PointerToPath(pointer), context, effectiveStrict, fineTuned, defs, map[string]struct{}{}, obj, pointer)
	if effectiveStrict {
		findings = append(findings, l.limitFindings(obj, pointer, context, defs)...)
		for _, f := range findings {
			if f.Kind == "additionalProperties" || f.Kind == "required" || f.Kind == "maxNesting" || f.Kind == "maxProperties" || f.Kind == "maxEnumValues" || f.Kind == "maxNameChars" {
				res.StrictViolations = append(res.StrictViolations, specvalidate.Violation{
					Side: specvalidate.SideRequest, Source: specvalidate.SourceStrictMode,
					Path: f.Path, Pointer: f.Pointer, Keyword: f.Kind, Message: f.Hint,
				})
				continue
			}
			res.Ignored = append(res.Ignored, f)
		}
	} else {
		res.Ignored = append(res.Ignored, findings...)
	}
	return res
}

func (l *Linter) limitFindings(root map[string]any, pointer, context string, defs map[string]any) []Finding {
	var out []Finding
	props, depth, enums, nameChars, maxEnumChars, maxEnumCount := measureSchema(root, defs, map[string]struct{}{}, 1)
	if l.Subset.Limits.MaxNesting > 0 && depth > l.Subset.Limits.MaxNesting {
		out = append(out, Finding{Path: specvalidate.PointerToPath(pointer), Pointer: pointer, Key: "maxNesting", Kind: "maxNesting", Context: context, EffectiveStrict: true, Severity: SeverityWarning, Action: ActionReject, Hint: fmt.Sprintf("schema nesting %d exceeds %d", depth, l.Subset.Limits.MaxNesting)})
	}
	if l.Subset.Limits.MaxProperties > 0 && props > l.Subset.Limits.MaxProperties {
		out = append(out, Finding{Path: specvalidate.PointerToPath(pointer), Pointer: pointer, Key: "maxProperties", Kind: "maxProperties", Context: context, EffectiveStrict: true, Severity: SeverityWarning, Action: ActionReject, Hint: fmt.Sprintf("%d properties exceeds %d", props, l.Subset.Limits.MaxProperties)})
	}
	if l.Subset.Limits.MaxEnumValues > 0 && enums > l.Subset.Limits.MaxEnumValues {
		out = append(out, Finding{Path: specvalidate.PointerToPath(pointer), Pointer: pointer, Key: "maxEnumValues", Kind: "maxEnumValues", Context: context, EffectiveStrict: true, Severity: SeverityWarning, Action: ActionReject, Hint: fmt.Sprintf("%d enum values exceeds %d", enums, l.Subset.Limits.MaxEnumValues)})
	}
	if l.Subset.Limits.MaxNameChars > 0 && nameChars > l.Subset.Limits.MaxNameChars {
		out = append(out, Finding{Path: specvalidate.PointerToPath(pointer), Pointer: pointer, Key: "maxNameChars", Kind: "maxNameChars", Context: context, EffectiveStrict: true, Severity: SeverityWarning, Action: ActionReject, Hint: "string-size limit exceeded"})
	}
	if l.Subset.Limits.MaxStringEnumValuesBeforeCharCap > 0 && maxEnumCount > l.Subset.Limits.MaxStringEnumValuesBeforeCharCap && maxEnumChars > l.Subset.Limits.MaxStringEnumChars {
		out = append(out, Finding{Path: specvalidate.PointerToPath(pointer), Pointer: pointer, Key: "maxStringEnumChars", Kind: "maxNameChars", Context: context, EffectiveStrict: true, Severity: SeverityWarning, Action: ActionReject, Hint: "string enum exceeds 15,000 characters"})
	}
	return out
}

func measureSchema(schema any, defs map[string]any, seen map[string]struct{}, depth int) (props, maxDepth, enums, nameChars, maxEnumChars, maxEnumCount int) {
	obj, ok := schema.(map[string]any)
	if !ok {
		return 0, depth, 0, 0, 0, 0
	}
	if ref, _ := obj["$ref"].(string); ref != "" {
		if _, ok := seen[ref]; ok {
			return 0, depth, 0, 0, 0, 0
		}
		seen[ref] = struct{}{}
		if target, ok := resolveRef(ref, defs, obj); ok {
			return measureSchema(target, defs, seen, depth)
		}
		return 0, depth, 0, 0, 0, 0
	}
	maxDepth = depth
	if p, ok := obj["properties"].(map[string]any); ok {
		props += len(p)
		for name, child := range p {
			nameChars += len(name)
			cp, cd, ce, cn, cme, cmc := measureSchema(child, mergeDefs(defs, obj), seen, depth+1)
			props += cp
			if cd > maxDepth {
				maxDepth = cd
			}
			enums += ce
			nameChars += cn
			if cme > maxEnumChars {
				maxEnumChars = cme
			}
			if cmc > maxEnumCount {
				maxEnumCount = cmc
			}
		}
	}
	if items := obj["items"]; items != nil {
		cp, cd, ce, cn, cme, cmc := measureSchema(items, mergeDefs(defs, obj), seen, depth+1)
		props += cp
		if cd > maxDepth {
			maxDepth = cd
		}
		enums += ce
		nameChars += cn
		if cme > maxEnumChars {
			maxEnumChars = cme
		}
		if cmc > maxEnumCount {
			maxEnumCount = cmc
		}
	}
	if anyOf, ok := obj["anyOf"].([]any); ok {
		for _, branch := range anyOf {
			cp, cd, ce, cn, cme, cmc := measureSchema(branch, mergeDefs(defs, obj), seen, depth+1)
			props += cp
			if cd > maxDepth {
				maxDepth = cd
			}
			enums += ce
			nameChars += cn
			if cme > maxEnumChars {
				maxEnumChars = cme
			}
			if cmc > maxEnumCount {
				maxEnumCount = cmc
			}
		}
	}
	if enum, ok := obj["enum"].([]any); ok {
		enums += len(enum)
		chars := 0
		for _, v := range enum {
			if s, ok := v.(string); ok {
				chars += len(s)
				nameChars += len(s)
			}
		}
		if len(enum) > maxEnumCount {
			maxEnumCount = len(enum)
			maxEnumChars = chars
		}
	}
	return props, maxDepth, enums, nameChars, maxEnumChars, maxEnumCount
}

func strictCompatible(schema map[string]any) bool {
	if schema == nil {
		return false
	}
	if _, ok := schema["anyOf"]; ok {
		return false
	}
	if typ, _ := schema["type"].(string); typ != "" && typ != "object" {
		return false
	}
	addl, hasAdd := schema["additionalProperties"]
	if !hasAdd {
		return false
	}
	if b, ok := addl.(bool); ok && b {
		return false
	}
	props, _ := schema["properties"].(map[string]any)
	req := requiredSet(schema["required"])
	for name := range props {
		if _, ok := req[name]; !ok {
			return false
		}
	}
	return true
}

func unknownKeys(obj map[string]any, known map[string]struct{}, pointer, context string, l *Linter) []Finding {
	if obj == nil {
		return nil
	}
	var out []Finding
	for _, key := range sortedKeys(obj) {
		if _, ok := known[key]; ok {
			continue
		}
		ptr := joinPtr(pointer, key)
		out = append(out, Finding{
			Path: specvalidate.PointerToPath(ptr), Pointer: ptr,
			Key: key, Kind: KindUnknownRequest, Context: context,
			UpstreamBehavior: BehaviorIgnored,
			Evidence:         "live:TestLiveSpec_UnknownInMessage_Chat",
			Severity:         SeverityWarning, Action: ActionReject,
			Hint: l.Subset.Hint("unknown_request_field"),
		})
	}
	return out
}

func (r *Result) merge(other Result) {
	r.Ignored = append(r.Ignored, other.Ignored...)
	r.StrictViolations = append(r.StrictViolations, other.StrictViolations...)
}

func boolVal(v any) (bool, bool) {
	b, ok := v.(bool)
	return b, ok
}

func firstObject(raw []byte) []byte {
	return specvalidateFirstJSON(raw)
}

func specvalidateFirstJSON(raw []byte) []byte {
	var v json.RawMessage
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	if err := dec.Decode(&v); err != nil {
		return raw
	}
	return v
}

func sortFindings(in []Finding) []Finding {
	sort.SliceStable(in, func(i, j int) bool {
		if in[i].Pointer != in[j].Pointer {
			return in[i].Pointer < in[j].Pointer
		}
		return in[i].Kind < in[j].Kind
	})
	return in
}

func capFindings(in []Finding) []Finding {
	if len(in) <= specvalidate.MaxFindings {
		return in
	}
	return append(in[:specvalidate.MaxFindings:specvalidate.MaxFindings], Finding{
		Key: "truncated", Kind: "truncated", Severity: SeverityInfo, Action: ActionReport,
		Hint: fmt.Sprintf("truncated; %d findings", len(in)),
	})
}

func specvalidateCap(in []specvalidate.Violation) []specvalidate.Violation {
	if len(in) <= specvalidate.MaxFindings {
		return in
	}
	return in[:specvalidate.MaxFindings]
}

var chatMessageKeys = map[string]struct{}{
	"role": {}, "content": {}, "name": {}, "refusal": {}, "tool_calls": {},
	"function_call": {}, "tool_call_id": {}, "audio": {},
}
var chatFunctionKeys = map[string]struct{}{
	"name": {}, "description": {}, "parameters": {}, "strict": {},
}
var chatContentPartKeys = map[string]struct{}{
	"type": {}, "text": {}, "image_url": {}, "input_audio": {}, "file": {}, "refusal": {},
}
var chatToolCallKeys = map[string]struct{}{
	"id": {}, "type": {}, "function": {}, "index": {},
}
var responsesFunctionKeys = map[string]struct{}{
	"type": {}, "name": {}, "async": {}, "description": {}, "parameters": {},
	"output_schema": {}, "strict": {}, "defer_loading": {}, "allowed_callers": {},
}
var responsesContentPartKeys = map[string]struct{}{
	"type": {}, "text": {}, "image_url": {}, "detail": {}, "file_id": {},
	"prompt_cache_breakpoint": {},
}

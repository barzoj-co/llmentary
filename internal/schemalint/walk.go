// SPDX-License-Identifier: AGPL-3.0-only

package schemalint

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/barzoj-co/llmentary/internal/specvalidate"
)

var structuralSkip = map[string]struct{}{
	"type": {}, "properties": {}, "required": {}, "additionalProperties": {},
	"unevaluatedProperties": {}, "items": {}, "anyOf": {}, "enum": {}, "const": {},
	"$defs": {}, "$ref": {}, "definitions": {}, "description": {},
	"default": {},
}

func (l *Linter) lintSchema(schema any, pointer, path, context string, effectiveStrict, fineTuned bool, defs map[string]any, seenRef map[string]struct{}) []Finding {
	return l.lintSchemaRoot(schema, pointer, path, context, effectiveStrict, fineTuned, defs, seenRef, nil, pointer)
}

func (l *Linter) lintSchemaRoot(schema any, pointer, path, context string, effectiveStrict, fineTuned bool, defs map[string]any, seenRef map[string]struct{}, root map[string]any, rootPointer string) []Finding {
	var out []Finding
	obj, ok := schema.(map[string]any)
	if !ok {
		return out
	}
	if seenRef == nil {
		seenRef = map[string]struct{}{}
	}
	if root == nil {
		root = obj
		rootPointer = pointer
	}
	if pointer != "" {
		visitKey := "ptr:" + pointer
		if _, seen := seenRef[visitKey]; seen {
			return out
		}
		seenRef[visitKey] = struct{}{}
	}
	if ref, _ := obj["$ref"].(string); ref != "" {
		if _, seen := seenRef[ref]; seen {
			return out
		}
		target, ok := resolveRef(ref, defs, root)
		if !ok {
			ptr := joinPtr(pointer, "$ref")
			out = append(out, Finding{
				Path: jsonPath(ptr), Pointer: ptr,
				Key: "$ref", Kind: KindUnresolvedRef, Context: context,
				EffectiveStrict: effectiveStrict, UpstreamBehavior: BehaviorNotGuar,
				Evidence: "docs:" + l.Subset.Sources.StructuredOutputs,
				Severity: SeverityWarning, Action: ActionReject, Hint: l.Subset.Hint("unresolved_ref"),
			})
			return out
		}
		seenRef[ref] = struct{}{}
		defPtr := rootPointer
		if ref == "#" {
			defPtr = rootPointer
		} else if strings.HasPrefix(ref, "#/") {
			defPtr = strings.TrimRight(rootPointer, "/") + strings.TrimPrefix(ref, "#")
		}
		return l.lintSchemaRoot(target, defPtr, jsonPath(defPtr), context, effectiveStrict, fineTuned, defs, seenRef, root, rootPointer)
	}

	mergedDefs := mergeDefs(defs, obj)

	if effectiveStrict {
		out = append(out, l.strictModeFindings(obj, pointer, path, context, fineTuned)...)
	}

	for key, val := range obj {
		if _, skip := structuralSkip[key]; skip {
			continue
		}
		if key == "format" {
			s, _ := val.(string)
			if s != "" && !l.Subset.FormatAllowed(s) {
				out = append(out, Finding{
					Path: jsonPath(joinPtr(pointer, "format")), Pointer: joinPtr(pointer, "format"),
					Key: "format", Kind: KindUnsupportedFormat, Context: context,
					EffectiveStrict: effectiveStrict, UpstreamBehavior: BehaviorNotGuar,
					Evidence: "docs:" + l.Subset.Sources.StructuredOutputs,
					Severity: SeverityWarning, Action: ActionReject,
					Hint: l.Subset.Hint("unsupported_format_value"),
				})
			}
			continue
		}
		kind, sev, action := l.Subset.Classify(key, fineTuned)
		if kind == "" {
			continue
		}
		behavior := BehaviorNotGuar
		evidence := "docs:" + l.Subset.Sources.StructuredOutputs
		if key == "enumDescriptions" || key == "enum_descriptions" {
			behavior = BehaviorIgnored
			evidence = "live:TestLiveTokens_EnumDescriptions_ChatTool"
		}
		f := Finding{
			Path: jsonPath(joinPtr(pointer, key)), Pointer: joinPtr(pointer, key),
			Key: key, Kind: kind, Context: context,
			EffectiveStrict: effectiveStrict, UpstreamBehavior: behavior,
			Evidence: evidence, Severity: sev, Action: action,
			Hint: l.Subset.Hint(key),
		}
		if key == "enumDescriptions" || key == "enum_descriptions" {
			f.SuggestedDescription = suggestDescription(obj, val)
		}
		out = append(out, f)
	}

	if enum, ok := obj["enum"].([]any); ok && suspiciousEnum(enum) {
		out = append(out, Finding{
			Path: jsonPath(joinPtr(pointer, "enum")), Pointer: joinPtr(pointer, "enum"),
			Key: "enum", Kind: KindSuspiciousEnum, Context: context,
			EffectiveStrict: effectiveStrict, UpstreamBehavior: BehaviorNotGuar,
			Evidence: "docs:" + l.Subset.Sources.StructuredOutputs,
			Severity: SeverityWarning, Action: ActionReject,
			Hint: l.Subset.Hint("suspicious_enum_values"),
		})
	}

	if props, ok := obj["properties"].(map[string]any); ok {
		for _, name := range sortedKeys(props) {
			out = append(out, l.lintSchemaRoot(props[name], joinPtr(pointer, "properties", name), path, context, effectiveStrict, fineTuned, mergedDefs, seenRef, root, rootPointer)...)
		}
	}
	if items, ok := obj["items"]; ok {
		out = append(out, l.lintSchemaRoot(items, joinPtr(pointer, "items"), path, context, effectiveStrict, fineTuned, mergedDefs, seenRef, root, rootPointer)...)
	}
	if anyOf, ok := obj["anyOf"].([]any); ok {
		for i, branch := range anyOf {
			out = append(out, l.lintSchemaRoot(branch, joinPtr(pointer, "anyOf", strconv.Itoa(i)), path, context, effectiveStrict, fineTuned, mergedDefs, seenRef, root, rootPointer)...)
		}
	}
	if add, ok := obj["additionalProperties"]; ok {
		if _, isBool := add.(bool); !isBool {
			out = append(out, l.lintSchemaRoot(add, joinPtr(pointer, "additionalProperties"), path, context, effectiveStrict, fineTuned, mergedDefs, seenRef, root, rootPointer)...)
		}
	}
	for _, defKey := range []string{"$defs", "definitions"} {
		if defsMap, ok := obj[defKey].(map[string]any); ok {
			for _, name := range sortedKeys(defsMap) {
				refKey := "#/" + defKey + "/" + name
				if _, seen := seenRef[refKey]; seen {
					continue
				}
				seenRef[refKey] = struct{}{}
				out = append(out, l.lintSchemaRoot(defsMap[name], joinPtr(pointer, defKey, name), path, context, effectiveStrict, fineTuned, mergedDefs, seenRef, root, rootPointer)...)
			}
		}
	}
	return out
}

func (l *Linter) strictModeFindings(obj map[string]any, pointer, path, context string, _ bool) []Finding {
	var out []Finding
	add := func(key, keyword, message string) {
		out = append(out, Finding{
			Path: jsonPath(pointer), Pointer: pointer, Key: key,
			Kind: keyword, Context: context, EffectiveStrict: true,
			UpstreamBehavior: BehaviorNotGuar,
			Evidence:         "docs:" + l.Subset.Sources.FunctionCallingStrict,
			Severity:         SeverityWarning, Action: ActionReject,
			Hint: message,
		})
	}
	if _, hasAnyOf := obj["anyOf"]; hasAnyOf && (pointer == "" || strings.Count(pointer, "/") <= 1) {
		// root-level anyOf is reported by the caller with source strict_mode
		_ = hasAnyOf
	}
	if addl, ok := obj["additionalProperties"]; ok {
		if b, isBool := addl.(bool); isBool && b {
			add("additionalProperties", "additionalProperties", "strict mode requires additionalProperties:false")
		}
	} else if _, hasProps := obj["properties"]; hasProps {
		add("additionalProperties", "additionalProperties", "strict mode requires additionalProperties:false")
	}
	if props, ok := obj["properties"].(map[string]any); ok {
		req := requiredSet(obj["required"])
		for _, name := range sortedKeys(props) {
			if _, ok := req[name]; !ok {
				add(name, "required", "strict mode requires every property to be listed in required")
			}
		}
	}
	return out
}

func resolveRef(ref string, defs map[string]any, root map[string]any) (any, bool) {
	if ref == "#" {
		return root, true
	}
	name := strings.TrimPrefix(ref, "#/$defs/")
	name = strings.TrimPrefix(name, "#/definitions/")
	name = strings.TrimPrefix(name, "#/components/schemas/")
	if i := strings.Index(name, "/"); i >= 0 {
		name = name[:i]
	}
	if defs != nil {
		if v, ok := defs[name]; ok {
			return v, true
		}
	}
	return nil, false
}

func mergeDefs(base map[string]any, obj map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range base {
		out[k] = v
	}
	for _, key := range []string{"$defs", "definitions"} {
		if m, ok := obj[key].(map[string]any); ok {
			for k, v := range m {
				out[k] = v
			}
		}
	}
	return out
}

func requiredSet(raw any) map[string]struct{} {
	out := map[string]struct{}{}
	arr, _ := raw.([]any)
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out[s] = struct{}{}
		}
	}
	return out
}

func suspiciousEnum(enum []any) bool {
	for _, v := range enum {
		switch v.(type) {
		case map[string]any, []any:
			return true
		}
	}
	return false
}

func suggestDescription(schema map[string]any, raw any) string {
	base, _ := schema["description"].(string)
	var parts []string
	switch m := raw.(type) {
	case map[string]any:
		order := enumKeyOrder(schema, m)
		for _, k := range order {
			parts = append(parts, fmt.Sprintf("%s: %v", k, m[k]))
		}
	case []any:
		var enums []any
		if e, ok := schema["enum"].([]any); ok {
			enums = e
		}
		for i, v := range m {
			label := strconv.Itoa(i)
			if i < len(enums) {
				label = fmt.Sprint(enums[i])
			}
			parts = append(parts, fmt.Sprintf("%s: %v", label, v))
		}
	}
	joined := strings.Join(parts, "; ")
	if strings.TrimSpace(base) == "" {
		return joined
	}
	if joined == "" {
		return base
	}
	base = strings.TrimSpace(base)
	base = strings.TrimRight(base, ".")
	return base + ". " + joined
}

func enumKeyOrder(schema map[string]any, values map[string]any) []string {
	var order []string
	seen := map[string]struct{}{}
	if enum, ok := schema["enum"].([]any); ok {
		for _, v := range enum {
			k := fmt.Sprint(v)
			if _, ok := values[k]; ok {
				order = append(order, k)
				seen[k] = struct{}{}
			}
		}
	}
	for _, k := range sortedKeys(values) {
		if _, ok := seen[k]; ok {
			continue
		}
		order = append(order, k)
	}
	return order
}

func joinPtr(base string, parts ...string) string {
	p := base
	for _, part := range parts {
		if p == "" {
			p = "/" + escapePtr(part)
			continue
		}
		p = strings.TrimRight(p, "/") + "/" + escapePtr(part)
	}
	return p
}

func escapePtr(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}

func jsonPath(pointer string) string {
	return specvalidate.PointerToPath(pointer)
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// SPDX-License-Identifier: AGPL-3.0-only

package specvalidate

import (
	"sort"
)

// Free-form user payloads that must stay open even if they declare properties.
var freeFormSchemaNames = map[string]struct{}{
	"FunctionParameters": {},
	"Metadata":           {},
}

var freeFormPropertyNames = map[string]struct{}{
	"metadata":   {},
	"logit_bias": {},
	"prediction": {},
	"extra_body": {},
	"schema":     {},
}

// ApplyClosedWorld sets unevaluatedProperties:false on every object schema that
// declares properties and does not already set additionalProperties or
// unevaluatedProperties. Free-form user payloads stay open.
// applyBothProfileCorrections adds live-API required fields that the pinned
// spec omits. Applied to both profiles so relaxed still predicts HTTP 400.
func applyBothProfileCorrections(spec map[string]any) {
	schemas := componentSchemas(spec)
	if schemas == nil {
		return
	}
	if cr, ok := schemas["CreateResponse"].(map[string]any); ok {
		req, _ := cr["required"].([]any)
		found := false
		for _, item := range req {
			if item == "model" {
				found = true
				break
			}
		}
		if !found {
			cr["required"] = append(req, "model")
		}
	}
}

func ApplyClosedWorld(spec map[string]any) ClosedWorldReport {
	report := ClosedWorldReport{}
	seenAlready := map[string]struct{}{}
	seenClosed := map[string]struct{}{}
	seenSkip := map[string]struct{}{}
	schemas := componentSchemas(spec)
	fragments := allOfFragmentNames(schemas)
	visited := map[string]struct{}{}
	for _, name := range []string{chatRequest, respRequest} {
		if raw, ok := schemas[name]; ok {
			walkClose(raw, name, name, &report, seenAlready, seenClosed, seenSkip, true, false, fragments, schemas, visited)
		}
	}
	report.AlreadyClosed = sortedKeys(seenAlready)
	report.ClosedByTransform = sortedKeys(seenClosed)
	report.SkippedFreeForm = sortedKeys(seenSkip)
	return report
}

func allOfFragmentNames(schemas map[string]any) map[string]struct{} {
	out := map[string]struct{}{}
	var walk func(any)
	walk = func(node any) {
		switch n := node.(type) {
		case map[string]any:
			if arr, ok := n["allOf"].([]any); ok {
				for _, item := range arr {
					m, ok := item.(map[string]any)
					if !ok {
						continue
					}
					ref, _ := m["$ref"].(string)
					if name, ok := schemaRefName(ref); ok {
						out[name] = struct{}{}
					}
				}
			}
			for _, v := range n {
				walk(v)
			}
		case []any:
			for _, v := range n {
				walk(v)
			}
		}
	}
	walk(schemas)
	return out
}

func schemaRefName(ref string) (string, bool) {
	const prefix = "#/components/schemas/"
	if !hasPrefix(ref, prefix) {
		return "", false
	}
	return ref[len(prefix):], true
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

func walkClose(node any, name, path string, report *ClosedWorldReport, already, closed, skip map[string]struct{}, named, inAllOf bool, fragments map[string]struct{}, schemas map[string]any, visited map[string]struct{}) {
	switch n := node.(type) {
	case map[string]any:
		if ref, _ := n["$ref"].(string); ref != "" {
			if target, ok := schemaRefName(ref); ok && schemas != nil {
				if _, seen := visited[target]; !seen {
					visited[target] = struct{}{}
					walkClose(schemas[target], target, target, report, already, closed, skip, true, inAllOf, fragments, schemas, visited)
				}
			}
		}
		_, hasProps := n["properties"].(map[string]any)
		_, hasAllOf := n["allOf"].([]any)
		_, hasAdd := n["additionalProperties"]
		_, hasUneval := n["unevaluatedProperties"]
		fragment := inAllOf || (named && fragments != nil && containsName(fragments, name))
		if isFreeForm(name, path, n) {
			skip[path] = struct{}{}
		} else if !fragment && (hasProps || hasAllOf) {
			if hasAdd || hasUneval {
				already[path] = struct{}{}
			} else {
				n["unevaluatedProperties"] = false
				closed[path] = struct{}{}
			}
		}
		if props, ok := n["properties"].(map[string]any); ok {
			for k, v := range props {
				walkClose(v, k, path+".properties."+k, report, already, closed, skip, false, false, fragments, schemas, visited)
			}
		}
		for _, key := range []string{"items", "not", "if", "then", "else", "additionalProperties", "unevaluatedProperties"} {
			if v, ok := n[key]; ok {
				walkClose(v, key, path+"."+key, report, already, closed, skip, false, false, fragments, schemas, visited)
			}
		}
		if arr, ok := n["allOf"].([]any); ok {
			for i, v := range arr {
				walkClose(v, "allOf", path+".allOf."+itoa(i), report, already, closed, skip, false, true, fragments, schemas, visited)
			}
		}
		for _, key := range []string{"anyOf", "oneOf", "prefixItems"} {
			if arr, ok := n[key].([]any); ok {
				for i, v := range arr {
					walkClose(v, key, path+"."+key+"."+itoa(i), report, already, closed, skip, false, false, fragments, schemas, visited)
				}
			}
		}
		if defs, ok := n["$defs"].(map[string]any); ok {
			for k, v := range defs {
				walkClose(v, k, path+".$defs."+k, report, already, closed, skip, true, false, fragments, schemas, visited)
			}
		}
		if defs, ok := n["definitions"].(map[string]any); ok {
			for k, v := range defs {
				walkClose(v, k, path+".definitions."+k, report, already, closed, skip, true, false, fragments, schemas, visited)
			}
		}
	case []any:
		for i, v := range n {
			walkClose(v, name, path+"."+itoa(i), report, already, closed, skip, false, inAllOf, fragments, schemas, visited)
		}
	}
}

func containsName(set map[string]struct{}, name string) bool {
	_, ok := set[name]
	return ok
}

func isFreeForm(name, path string, schema map[string]any) bool {
	if _, ok := freeFormSchemaNames[name]; ok {
		return true
	}
	if _, ok := freeFormPropertyNames[name]; ok {
		// A property named schema/metadata is free-form when it is a map
		// (additionalProperties already set or no typed properties of its own
		// that look like a closed object). Named schema objects still close.
		if label, _ := schema["x-oaiTypeLabel"].(string); label == "map" {
			return true
		}
		if _, hasAdd := schema["additionalProperties"]; hasAdd {
			return true
		}
	}
	if label, _ := schema["x-oaiTypeLabel"].(string); label == "map" {
		return true
	}
	_ = path
	return false
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}

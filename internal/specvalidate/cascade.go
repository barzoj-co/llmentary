// SPDX-License-Identifier: AGPL-3.0-only

package specvalidate

import (
	"strings"
)

// objectProps maps a generalized JSON Pointer pattern (array indexes
// replaced with *) to the property names declared on that object.
type objectProps map[string]map[string]struct{}

func collectKnownProperties(spec map[string]any, root string) objectProps {
	schemas := componentSchemas(spec)
	if schemas == nil || root == "" {
		return nil
	}
	out := objectProps{}
	visited := map[string]struct{}{}
	var walk func(node any, pattern string, depth int)
	walk = func(node any, pattern string, depth int) {
		if depth > 10 {
			return
		}
		m, ok := node.(map[string]any)
		if !ok {
			return
		}
		if ref, _ := m["$ref"].(string); ref != "" {
			name, ok := schemaRefName(ref)
			if !ok {
				return
			}
			key := name + "@" + pattern
			if _, seen := visited[key]; seen {
				return
			}
			visited[key] = struct{}{}
			walk(schemas[name], pattern, depth+1)
			return
		}
		if props, ok := m["properties"].(map[string]any); ok {
			if out[pattern] == nil {
				out[pattern] = map[string]struct{}{}
			}
			for k, v := range props {
				out[pattern][k] = struct{}{}
				child := pattern + "/" + k
				if pattern == "" {
					child = "/" + k
				}
				walk(v, child, depth+1)
			}
		}
		if items, ok := m["items"]; ok {
			walk(items, joinPattern(pattern, "*"), depth+1)
		}
		if arr, ok := m["prefixItems"].([]any); ok {
			for _, v := range arr {
				walk(v, joinPattern(pattern, "*"), depth+1)
			}
		}
		for _, key := range []string{"anyOf", "oneOf", "allOf"} {
			arr, ok := m[key].([]any)
			if !ok {
				continue
			}
			for _, v := range arr {
				walk(v, pattern, depth+1)
			}
		}
	}
	walk(schemas[root], "", 0)
	return out
}

func joinPattern(parent, child string) string {
	if parent == "" {
		return "/" + child
	}
	return parent + "/" + child
}

func (p objectProps) has(parentPattern, field string) bool {
	if p == nil || field == "" {
		return false
	}
	names := p[parentPattern]
	if names == nil {
		return false
	}
	_, ok := names[field]
	return ok
}

func splitPointerField(pointer string) (parent, field string) {
	field = lastPointerField(pointer)
	if field == "" {
		return pointer, ""
	}
	trim := strings.TrimRight(pointer, "/")
	idx := strings.LastIndex(trim, "/")
	if idx <= 0 {
		return "", field
	}
	return trim[:idx], field
}

func generalizePointer(pointer string) string {
	if pointer == "" || pointer == "/" {
		return ""
	}
	parts := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
	for i, part := range parts {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		if isIndex(part) {
			parts[i] = "*"
		}
	}
	return "/" + strings.Join(parts, "/")
}

func arrayItemPrefix(pointer string) string {
	pointer = strings.TrimRight(pointer, "/")
	if pointer == "" {
		return ""
	}
	parts := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
	for i := len(parts) - 1; i >= 0; i-- {
		if isIndex(parts[i]) {
			return "/" + strings.Join(parts[:i+1], "/")
		}
	}
	return ""
}

func isLosingNullType(item schemaErr) bool {
	if item.keyword != "type" {
		return false
	}
	msg := strings.ToLower(item.message)
	return strings.Contains(msg, "want null")
}

func underPointer(pointer, root string) bool {
	if root == "" {
		return false
	}
	return pointer == root || strings.HasPrefix(pointer, root+"/")
}

func isOneOfBranchNoise(item schemaErr, itemPrefix string, losingRoots []string) bool {
	if itemPrefix == "" {
		return false
	}
	for _, root := range losingRoots {
		if underPointer(item.pointer, root) {
			return true
		}
	}
	switch item.keyword {
	case "required":
		return item.pointer == itemPrefix
	case "type", "enum":
		if item.pointer == itemPrefix {
			return true
		}
		rest := strings.TrimPrefix(item.pointer, itemPrefix+"/")
		if rest == "" || strings.Contains(rest, "/") {
			return false
		}
		switch rest {
		case "role", "type", "content", "status":
			return true
		}
	}
	return false
}

func dedupeSchemaErr(in []schemaErr) []schemaErr {
	if len(in) < 2 {
		return in
	}
	type key struct{ pointer, keyword, message string }
	seen := map[key]struct{}{}
	out := make([]schemaErr, 0, len(in))
	for _, item := range in {
		k := key{item.pointer, item.keyword, item.message}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, item)
	}
	return out
}

func filterCascadedFindings(in []schemaErr, known objectProps) []schemaErr {
	if len(in) == 0 {
		return in
	}
	filtered := in[:0]
	for _, item := range in {
		if item.keyword == "unevaluatedProperties" && known != nil {
			parent, field := splitPointerField(item.pointer)
			if field != "" && known.has(generalizePointer(parent), field) {
				continue
			}
			if parent == "" || parent == "/" {
				keep := make([]string, 0, len(item.extraNames))
				for _, name := range item.extraNames {
					if !known.has("", name) {
						keep = append(keep, name)
					}
				}
				if len(item.extraNames) > 0 && len(keep) == 0 {
					continue
				}
				item.extraNames = keep
			}
		}
		filtered = append(filtered, item)
	}
	return collapseOneOfBranchNoise(dedupeSchemaErr(filtered))
}

func collapseOneOfBranchNoise(in []schemaErr) []schemaErr {
	if len(in) < 2 {
		return in
	}
	groups := map[string][]int{}
	for i, item := range in {
		if prefix := arrayItemPrefix(item.pointer); prefix != "" {
			groups[prefix] = append(groups[prefix], i)
		}
	}
	drop := make([]bool, len(in))
	for prefix, idxs := range groups {
		var losingRoots []string
		for _, i := range idxs {
			if isLosingNullType(in[i]) && underPointer(in[i].pointer, prefix) {
				losingRoots = append(losingRoots, in[i].pointer)
			}
		}
		var specific, noise []int
		for _, i := range idxs {
			if isOneOfBranchNoise(in[i], prefix, losingRoots) {
				noise = append(noise, i)
			} else {
				specific = append(specific, i)
			}
		}
		if len(specific) > 0 {
			for _, i := range noise {
				drop[i] = true
			}
			continue
		}
		if len(noise) < 2 {
			continue
		}
		keep := noise[0]
		for _, i := range noise[1:] {
			if preferOneOfNoiseRootCause(in[i], in[keep]) {
				keep = i
			}
		}
		for _, i := range noise {
			if i != keep {
				drop[i] = true
			}
		}
	}
	out := make([]schemaErr, 0, len(in))
	for i, item := range in {
		if drop[i] {
			continue
		}
		out = append(out, item)
	}
	return out
}

// preferOneOfNoiseRootCause picks the matching-variant finding when an
// array item produced only oneOf-branch noise. Prefer an item-level
// required (e.g. FunctionTool missing strict) over discriminator enums
// and EmptyModelParam "want null" cascades. Among requireds, keep the
// first in input order (the matching variant is listed first).
func preferOneOfNoiseRootCause(a, b schemaErr) bool {
	if a.keyword == "required" && b.keyword != "required" {
		return true
	}
	return false
}

func pointerDepth(pointer string) int {
	pointer = strings.Trim(pointer, "/")
	if pointer == "" {
		return 0
	}
	return strings.Count(pointer, "/") + 1
}

func keywordSpecificity(keyword string) int {
	switch keyword {
	case "enum", "const", "type", "required":
		return 3
	case "format", "pattern", "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum",
		"multipleOf", "minLength", "maxLength", "minItems", "maxItems":
		return 2
	case "unevaluatedProperties", "additionalProperties":
		return 0
	default:
		return 1
	}
}

func violationMoreSpecific(a, b Violation) bool {
	if a.Side != b.Side {
		return a.Side < b.Side
	}
	da, db := pointerDepth(a.Pointer), pointerDepth(b.Pointer)
	if da != db {
		return da > db
	}
	sa, sb := keywordSpecificity(a.Keyword), keywordSpecificity(b.Keyword)
	if sa != sb {
		return sa > sb
	}
	if a.Pointer != b.Pointer {
		return a.Pointer < b.Pointer
	}
	return a.Keyword < b.Keyword
}

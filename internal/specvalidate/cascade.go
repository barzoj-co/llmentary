// SPDX-License-Identifier: AGPL-3.0-only

package specvalidate

import (
	"strconv"
	"strings"
)

// objectProps maps a generalized JSON Pointer pattern (array indexes
// replaced with *) to the property names declared on that object.
type objectProps map[string]map[string]struct{}

const knownPropsDepthCap = 32

type discVariants struct {
	key   string
	props map[string]map[string]struct{}
}

func collectKnownProperties(spec map[string]any, root string) objectProps {
	union, _ := collectKnownBundle(spec, root)
	return union
}

func collectKnownBundle(spec map[string]any, root string) (objectProps, map[string]*discVariants) {
	schemas := componentSchemas(spec)
	if schemas == nil || root == "" {
		return nil, nil
	}
	out := objectProps{}
	variants := map[string]*discVariants{}
	visited := map[string]struct{}{}
	inStack := map[string]struct{}{}
	var walk func(node any, pattern string, depth int, skipProps bool)
	walk = func(node any, pattern string, depth int, skipProps bool) {
		if depth > knownPropsDepthCap {
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
			if _, cyc := inStack[name]; cyc {
				recordKnownProps(out, schemas[name], pattern, skipProps)
				return
			}
			key := name + "@" + pattern
			if _, seen := visited[key]; seen {
				return
			}
			visited[key] = struct{}{}
			inStack[name] = struct{}{}
			walk(schemas[name], pattern, depth, skipProps)
			delete(inStack, name)
			return
		}
		if props, ok := m["properties"].(map[string]any); ok {
			if !skipProps {
				if out[pattern] == nil {
					out[pattern] = map[string]struct{}{}
				}
				for k := range props {
					out[pattern][k] = struct{}{}
				}
			}
			for k, v := range props {
				child := pattern + "/" + k
				if pattern == "" {
					child = "/" + k
				}
				walk(v, child, depth+1, false)
			}
		}
		if items, ok := m["items"]; ok {
			walk(items, joinPattern(pattern, "*"), depth+1, false)
		}
		if arr, ok := m["prefixItems"].([]any); ok {
			for _, v := range arr {
				walk(v, joinPattern(pattern, "*"), depth+1, false)
			}
		}
		if arr, ok := m["allOf"].([]any); ok {
			for _, v := range arr {
				walk(v, pattern, depth, skipProps)
			}
		}
		for _, key := range []string{"anyOf", "oneOf"} {
			arr, ok := m[key].([]any)
			if !ok {
				continue
			}
			for _, v := range arr {
				recordVariantProps(variants, schemas, v, pattern, inStack)
				walk(v, pattern, depth, skipProps)
			}
		}
	}
	if root != "" {
		inStack[root] = struct{}{}
	}
	walk(schemas[root], "", 0, false)
	if root != "" {
		delete(inStack, root)
	}
	return out, variants
}

func recordKnownProps(out objectProps, node any, pattern string, skipProps bool) {
	if skipProps {
		return
	}
	m, ok := node.(map[string]any)
	if !ok {
		return
	}
	props, ok := m["properties"].(map[string]any)
	if !ok {
		return
	}
	if out[pattern] == nil {
		out[pattern] = map[string]struct{}{}
	}
	for k := range props {
		out[pattern][k] = struct{}{}
	}
}

func directKnownProps(schemas map[string]any, node any, inStack map[string]struct{}, dest map[string]struct{}) {
	m, ok := node.(map[string]any)
	if !ok {
		return
	}
	if ref, _ := m["$ref"].(string); ref != "" {
		name, ok := schemaRefName(ref)
		if !ok {
			return
		}
		if _, cyc := inStack[name]; cyc {
			return
		}
		inStack[name] = struct{}{}
		directKnownProps(schemas, schemas[name], inStack, dest)
		delete(inStack, name)
		return
	}
	if props, ok := m["properties"].(map[string]any); ok {
		for k := range props {
			dest[k] = struct{}{}
		}
	}
	if arr, ok := m["allOf"].([]any); ok {
		for _, v := range arr {
			directKnownProps(schemas, v, inStack, dest)
		}
	}
}

func recordVariantProps(variants map[string]*discVariants, schemas map[string]any, node any, pattern string, inStack map[string]struct{}) {
	props := map[string]struct{}{}
	directKnownProps(schemas, node, inStack, props)
	key, value := branchDiscriminator(schemas, node, inStack)
	if key == "" || value == "" || len(props) == 0 {
		return
	}
	cur := variants[pattern]
	if cur == nil {
		cur = &discVariants{key: key, props: map[string]map[string]struct{}{}}
		variants[pattern] = cur
	}
	if cur.key != key {
		return
	}
	if cur.props[value] == nil {
		cur.props[value] = map[string]struct{}{}
	}
	for k := range props {
		cur.props[value][k] = struct{}{}
	}
}

func branchDiscriminator(schemas map[string]any, node any, inStack map[string]struct{}) (key, value string) {
	m, ok := node.(map[string]any)
	if !ok {
		return "", ""
	}
	if ref, _ := m["$ref"].(string); ref != "" {
		name, ok := schemaRefName(ref)
		if !ok {
			return "", ""
		}
		if _, cyc := inStack[name]; cyc {
			return "", ""
		}
		inStack[name] = struct{}{}
		key, value = branchDiscriminator(schemas, schemas[name], inStack)
		delete(inStack, name)
		return key, value
	}
	props, _ := m["properties"].(map[string]any)
	for _, disc := range []string{"role", "type"} {
		p, _ := props[disc].(map[string]any)
		if p == nil {
			continue
		}
		if c, ok := p["const"].(string); ok && c != "" {
			return disc, c
		}
		if e, ok := p["enum"].([]any); ok && len(e) == 1 {
			if s, ok := e[0].(string); ok && s != "" {
				return disc, s
			}
		}
	}
	return "", ""
}

func instanceObject(inst any, pointer string) map[string]any {
	cur := inst
	pointer = strings.Trim(pointer, "/")
	if pointer != "" {
		for _, part := range strings.Split(pointer, "/") {
			part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
			switch c := cur.(type) {
			case map[string]any:
				cur = c[part]
			case []any:
				i, err := strconv.Atoi(part)
				if err != nil || i < 0 || i >= len(c) {
					return nil
				}
				cur = c[i]
			default:
				return nil
			}
		}
	}
	m, _ := cur.(map[string]any)
	return m
}

func variantHasField(variants map[string]*discVariants, parentPointer, field string, inst any) (known bool, ok bool) {
	if variants == nil || inst == nil || field == "" {
		return false, false
	}
	pattern := generalizePointer(parentPointer)
	v := variants[pattern]
	if v == nil {
		return false, false
	}
	obj := instanceObject(inst, parentPointer)
	if obj == nil {
		return false, false
	}
	disc, _ := obj[v.key].(string)
	if disc == "" {
		return false, false
	}
	names := v.props[disc]
	if names == nil {
		return false, true
	}
	_, has := names[field]
	return has, true
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
	return filterCascadedFindingsAt(in, known, nil, nil)
}

func filterCascadedFindingsAt(in []schemaErr, known objectProps, variants map[string]*discVariants, inst any) []schemaErr {
	if len(in) == 0 {
		return in
	}
	filtered := in[:0]
	for _, item := range in {
		if item.keyword == "unevaluatedProperties" && known != nil {
			parent, field := splitPointerField(item.pointer)
			if field != "" && known.has(generalizePointer(parent), field) {
				if has, ok := variantHasField(variants, parent, field, inst); ok && !has {
					filtered = append(filtered, item)
					continue
				}
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

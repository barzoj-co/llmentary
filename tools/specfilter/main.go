// SPDX-License-Identifier: AGPL-3.0-only

// specfilter extracts a deterministic OpenAPI sub-spec: the operations listed
// in operations.txt plus the transitive $ref closure over components/*, and
// fills missing discriminator mappings from each variant's type enum/const.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
)

func main() {
	specPath := flag.String("spec", "", "path to the full OpenAPI JSON document")
	opsPath := flag.String("ops", "", "path to operations.txt (one operationId per line)")
	outPath := flag.String("out", "", "path to write the filtered JSON document")
	flag.Parse()
	if *specPath == "" || *opsPath == "" || *outPath == "" {
		fmt.Fprintln(os.Stderr, "usage: specfilter -spec openapi.json -ops operations.txt -out sub.json")
		os.Exit(2)
	}
	if err := filterSpec(*specPath, *opsPath, *outPath); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func filterSpec(specPath, opsPath, outPath string) error {
	raw, err := os.ReadFile(specPath)
	if err != nil {
		return err
	}
	var spec map[string]any
	if err := json.Unmarshal(raw, &spec); err != nil {
		return fmt.Errorf("parse spec: %w", err)
	}
	wanted, err := readOperationIDs(opsPath)
	if err != nil {
		return err
	}
	filtered, err := extract(spec, wanted)
	if err != nil {
		return err
	}
	ensureDiscriminatorMappings(filtered)
	out, err := json.MarshalIndent(filtered, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	return os.WriteFile(outPath, out, 0o644)
}

func readOperationIDs(path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ids []string
	seen := map[string]struct{}{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if _, ok := seen[line]; ok {
			continue
		}
		seen[line] = struct{}{}
		ids = append(ids, line)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("no operationIds in %s", path)
	}
	sort.Strings(ids)
	return ids, nil
}

func extract(spec map[string]any, wanted []string) (map[string]any, error) {
	want := map[string]struct{}{}
	for _, id := range wanted {
		want[id] = struct{}{}
	}
	paths, _ := spec["paths"].(map[string]any)
	selectedPaths := map[string]any{}
	found := map[string]struct{}{}
	for _, pathName := range sortedKeys(paths) {
		item, _ := paths[pathName].(map[string]any)
		if item == nil {
			continue
		}
		kept := map[string]any{}
		for _, key := range sortedKeys(item) {
			if isPathLevelKey(key) {
				continue
			}
			op, _ := item[key].(map[string]any)
			if op == nil {
				continue
			}
			id, _ := op["operationId"].(string)
			if _, ok := want[id]; !ok {
				continue
			}
			kept[key] = op
			found[id] = struct{}{}
		}
		if len(kept) == 0 {
			continue
		}
		for _, key := range []string{"summary", "description", "servers", "parameters", "$ref"} {
			if v, ok := item[key]; ok {
				kept[key] = v
			}
		}
		selectedPaths[pathName] = kept
	}
	var missing []string
	for _, id := range wanted {
		if _, ok := found[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("operationIds not found in spec: %s", strings.Join(missing, ", "))
	}

	out := map[string]any{
		"openapi": spec["openapi"],
		"info":    spec["info"],
		"paths":   selectedPaths,
	}
	if v, ok := spec["servers"]; ok {
		out["servers"] = v
	}
	if v, ok := spec["security"]; ok {
		out["security"] = v
	}

	needed := collectComponentRefs(selectedPaths)
	if sec, ok := spec["security"]; ok {
		collectRefs(sec, needed)
	}
	components, _ := spec["components"].(map[string]any)
	closeComponentRefs(components, needed)

	filteredComponents := map[string]any{}
	for _, kind := range sortedKeys(components) {
		bucket, _ := components[kind].(map[string]any)
		if bucket == nil {
			continue
		}
		kept := map[string]any{}
		for _, name := range sortedKeys(bucket) {
			if needed[componentKey(kind, name)] {
				kept[name] = bucket[name]
			}
		}
		if len(kept) > 0 {
			filteredComponents[kind] = kept
		}
	}
	if len(filteredComponents) > 0 {
		out["components"] = filteredComponents
	}

	if tags, ok := spec["tags"].([]any); ok {
		used := usedTagNames(selectedPaths)
		var kept []any
		for _, tag := range tags {
			m, _ := tag.(map[string]any)
			name, _ := m["name"].(string)
			if _, ok := used[name]; ok {
				kept = append(kept, tag)
			}
		}
		if len(kept) > 0 {
			out["tags"] = kept
		}
	}
	return out, nil
}

func isPathLevelKey(key string) bool {
	switch key {
	case "summary", "description", "servers", "parameters", "$ref":
		return true
	default:
		return false
	}
}

func collectComponentRefs(node any) map[string]bool {
	needed := map[string]bool{}
	collectRefs(node, needed)
	return needed
}

func collectRefs(node any, needed map[string]bool) {
	switch v := node.(type) {
	case map[string]any:
		if ref, ok := v["$ref"].(string); ok {
			if kind, name, ok := parseComponentRef(ref); ok {
				needed[componentKey(kind, name)] = true
			}
		}
		for _, value := range v {
			collectRefs(value, needed)
		}
	case []any:
		for _, item := range v {
			collectRefs(item, needed)
		}
	}
}

func closeComponentRefs(components map[string]any, needed map[string]bool) {
	if components == nil {
		return
	}
	changed := true
	for changed {
		changed = false
		for _, kind := range sortedKeys(components) {
			bucket, _ := components[kind].(map[string]any)
			for _, name := range sortedKeys(bucket) {
				if !needed[componentKey(kind, name)] {
					continue
				}
				before := len(needed)
				collectRefs(bucket[name], needed)
				if disc, ok := asMap(bucket[name])["discriminator"].(map[string]any); ok {
					if mapping, ok := disc["mapping"].(map[string]any); ok {
						for _, target := range mapping {
							if ref, ok := target.(string); ok {
								if k, n, ok := parseComponentRef(ref); ok {
									needed[componentKey(k, n)] = true
								}
							}
						}
					}
				}
				if len(needed) != before {
					changed = true
				}
			}
		}
	}
}

func parseComponentRef(ref string) (kind, name string, ok bool) {
	const prefix = "#/components/"
	if !strings.HasPrefix(ref, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(ref, prefix)
	kind, name, ok = strings.Cut(rest, "/")
	if !ok || kind == "" || name == "" {
		return "", "", false
	}
	if idx := strings.Index(name, "/"); idx >= 0 {
		name = name[:idx]
	}
	return kind, name, true
}

func componentKey(kind, name string) string {
	return kind + "/" + name
}

func usedTagNames(paths map[string]any) map[string]struct{} {
	used := map[string]struct{}{}
	for _, item := range paths {
		m, _ := item.(map[string]any)
		for _, op := range m {
			om, _ := op.(map[string]any)
			tags, _ := om["tags"].([]any)
			for _, tag := range tags {
				if s, ok := tag.(string); ok {
					used[s] = struct{}{}
				}
			}
		}
	}
	return used
}

func ensureDiscriminatorMappings(spec map[string]any) {
	components, _ := spec["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	if schemas == nil {
		return
	}
	for _, name := range sortedKeys(schemas) {
		schema, _ := schemas[name].(map[string]any)
		if schema == nil {
			continue
		}
		disc, _ := schema["discriminator"].(map[string]any)
		if disc == nil {
			continue
		}
		prop, _ := disc["propertyName"].(string)
		if prop == "" {
			continue
		}
		mapping, _ := disc["mapping"].(map[string]any)
		if mapping == nil {
			mapping = map[string]any{}
		}
		for _, variant := range schemaVariants(schema) {
			ref, variantSchema := resolveVariant(variant, schemas)
			if ref == "" || variantSchema == nil {
				continue
			}
			for _, value := range discriminatorValues(variantSchema, prop, schemas) {
				if _, exists := mapping[value]; !exists {
					mapping[value] = ref
				}
			}
		}
		if len(mapping) > 0 {
			disc["mapping"] = mapping
			schema["discriminator"] = disc
		}
	}
}

func schemaVariants(schema map[string]any) []any {
	var out []any
	for _, key := range []string{"anyOf", "oneOf"} {
		if items, ok := schema[key].([]any); ok {
			out = append(out, items...)
		}
	}
	return out
}

func resolveVariant(variant any, schemas map[string]any) (string, map[string]any) {
	m, _ := variant.(map[string]any)
	if m == nil {
		return "", nil
	}
	if ref, ok := m["$ref"].(string); ok {
		if kind, name, ok := parseComponentRef(ref); ok && kind == "schemas" {
			resolved, _ := schemas[name].(map[string]any)
			return "#/components/schemas/" + name, resolved
		}
		return ref, nil
	}
	return "", m
}

func discriminatorValues(schema map[string]any, prop string, schemas map[string]any) []string {
	if schema == nil {
		return nil
	}
	if field := findProperty(schema, prop, schemas, 0); field != nil {
		if values := enumOrConst(field); len(values) > 0 {
			return values
		}
	}
	return nil
}

func findProperty(schema map[string]any, prop string, schemas map[string]any, depth int) map[string]any {
	if schema == nil || depth > 8 {
		return nil
	}
	if ref, ok := schema["$ref"].(string); ok {
		if kind, name, ok := parseComponentRef(ref); ok && kind == "schemas" {
			return findProperty(asMap(schemas[name]), prop, schemas, depth+1)
		}
	}
	if props, ok := schema["properties"].(map[string]any); ok {
		if field, ok := props[prop].(map[string]any); ok {
			return field
		}
	}
	for _, key := range []string{"allOf", "anyOf", "oneOf"} {
		items, _ := schema[key].([]any)
		for _, item := range items {
			if field := findProperty(asMap(item), prop, schemas, depth+1); field != nil {
				return field
			}
		}
	}
	return nil
}

func enumOrConst(field map[string]any) []string {
	if field == nil {
		return nil
	}
	if c, ok := field["const"]; ok {
		if s, ok := c.(string); ok {
			return []string{s}
		}
	}
	if raw, ok := field["enum"].([]any); ok {
		var out []string
		for _, v := range raw {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

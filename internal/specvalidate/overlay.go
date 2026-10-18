// SPDX-License-Identifier: AGPL-3.0-only

package specvalidate

import (
	"fmt"
	"sort"

	"gopkg.in/yaml.v3"
)

type Overlay struct {
	Overlay   string         `yaml:"overlay"`
	Info      OverlayInfo    `yaml:"info"`
	Prechecks OverlayChecks  `yaml:"prechecks"`
	Entries   []OverlayEntry `yaml:"entries"`
}

type OverlayInfo struct {
	Title   string `yaml:"title"`
	Version string `yaml:"version"`
	Added   string `yaml:"added"`
}

type OverlayChecks struct {
	Skip []OverlaySkip `yaml:"skip"`
}

type OverlaySkip struct {
	ID       string          `yaml:"id"`
	Why      string          `yaml:"why"`
	Evidence OverlayEvidence `yaml:"evidence"`
	Added    string          `yaml:"added"`
}

type OverlayEntry struct {
	ID       string          `yaml:"id"`
	Why      string          `yaml:"why"`
	Evidence OverlayEvidence `yaml:"evidence"`
	Added    string          `yaml:"added"`
	Action   string          `yaml:"action"`
	Schemas  []string        `yaml:"schemas"`
	Property string          `yaml:"property"`
}

type OverlayEvidence struct {
	LiveTest string `yaml:"live_test"`
	URL      string `yaml:"url"`
}

func ParseOverlay(raw []byte) (Overlay, error) {
	var ov Overlay
	if err := yaml.Unmarshal(raw, &ov); err != nil {
		return Overlay{}, fmt.Errorf("relax-overlay.yaml: %w", err)
	}
	if len(ov.Entries) == 0 {
		return Overlay{}, fmt.Errorf("relax-overlay.yaml: no entries")
	}
	for i, e := range ov.Entries {
		if e.ID == "" {
			return Overlay{}, fmt.Errorf("relax-overlay.yaml: entry %d missing id", i)
		}
		if e.Evidence.LiveTest == "" {
			return Overlay{}, fmt.Errorf("relax-overlay.yaml: entry %q missing evidence.live_test", e.ID)
		}
		if e.Added == "" {
			return Overlay{}, fmt.Errorf("relax-overlay.yaml: entry %q missing added date", e.ID)
		}
		if e.Action == "" {
			return Overlay{}, fmt.Errorf("relax-overlay.yaml: entry %q missing action", e.ID)
		}
	}
	return ov, nil
}

func ApplyOverlay(spec map[string]any, ov Overlay) error {
	schemas := componentSchemas(spec)
	if schemas == nil {
		return fmt.Errorf("spec has no components.schemas")
	}
	for _, e := range ov.Entries {
		for _, name := range e.Schemas {
			node, ok := schemas[name].(map[string]any)
			if !ok {
				continue
			}
			switch e.Action {
			case "reopen_object":
				delete(node, "additionalProperties")
				delete(node, "unevaluatedProperties")
			case "remove_required":
				if e.Property == "" {
					return fmt.Errorf("overlay %q: remove_required needs property", e.ID)
				}
				node["required"] = removeRequired(node["required"], e.Property)
			default:
				return fmt.Errorf("overlay %q: unknown action %q", e.ID, e.Action)
			}
			schemas[name] = node
		}
	}
	return nil
}

func (ov Overlay) SkipsPrecheck(keyword string) bool {
	for _, s := range ov.Prechecks.Skip {
		if s.ID == keyword {
			return true
		}
	}
	return false
}

func (ov Overlay) EntryIDs() []string {
	ids := make([]string, 0, len(ov.Entries))
	for _, e := range ov.Entries {
		ids = append(ids, e.ID)
	}
	sort.Strings(ids)
	return ids
}

func removeRequired(raw any, prop string) []any {
	arr, _ := raw.([]any)
	out := make([]any, 0, len(arr))
	for _, item := range arr {
		s, _ := item.(string)
		if s == prop {
			continue
		}
		out = append(out, item)
	}
	return out
}

func componentSchemas(spec map[string]any) map[string]any {
	comp, _ := spec["components"].(map[string]any)
	if comp == nil {
		return nil
	}
	schemas, _ := comp["schemas"].(map[string]any)
	return schemas
}

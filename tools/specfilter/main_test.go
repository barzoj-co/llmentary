// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFilterCollectsTransitiveRefsAndDiscriminatorMapping(t *testing.T) {
	dir := t.TempDir()
	spec := map[string]any{
		"openapi": "3.1.0",
		"info":    map[string]any{"title": "t", "version": "1"},
		"paths": map[string]any{
			"/widgets": map[string]any{
				"post": map[string]any{
					"operationId": "createWidget",
					"requestBody": map[string]any{
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{"$ref": "#/components/schemas/Widget"},
							},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{"$ref": "#/components/schemas/StreamEvent"},
								},
							},
						},
					},
				},
			},
		},
		"components": map[string]any{
			"schemas": map[string]any{
				"Widget": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name": map[string]any{"type": "string"},
					},
				},
				"Unused": map[string]any{"type": "string"},
				"StreamEvent": map[string]any{
					"anyOf": []any{
						map[string]any{"$ref": "#/components/schemas/AlphaEvent"},
						map[string]any{"$ref": "#/components/schemas/BetaEvent"},
					},
					"discriminator": map[string]any{"propertyName": "type"},
				},
				"AlphaEvent": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"type": map[string]any{"type": "string", "enum": []any{"alpha.delta"}},
					},
				},
				"BetaEvent": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"type": map[string]any{"type": "string", "const": "beta.done"},
					},
				},
			},
		},
	}
	specPath := filepath.Join(dir, "openapi.json")
	writeJSON(t, specPath, spec)
	opsPath := filepath.Join(dir, "operations.txt")
	if err := os.WriteFile(opsPath, []byte("createWidget\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out1 := filepath.Join(dir, "out1.json")
	out2 := filepath.Join(dir, "out2.json")
	if err := filterSpec(specPath, opsPath, out1); err != nil {
		t.Fatal(err)
	}
	if err := filterSpec(specPath, opsPath, out2); err != nil {
		t.Fatal(err)
	}
	a, _ := os.ReadFile(out1)
	b, _ := os.ReadFile(out2)
	if string(a) != string(b) {
		t.Fatalf("filter is not deterministic")
	}
	var got map[string]any
	if err := json.Unmarshal(a, &got); err != nil {
		t.Fatal(err)
	}
	schemas := got["components"].(map[string]any)["schemas"].(map[string]any)
	if _, ok := schemas["Unused"]; ok {
		t.Fatalf("unused schema was kept")
	}
	if _, ok := schemas["Widget"]; !ok {
		t.Fatalf("widget schema missing")
	}
	disc := schemas["StreamEvent"].(map[string]any)["discriminator"].(map[string]any)
	mapping := disc["mapping"].(map[string]any)
	if mapping["alpha.delta"] != "#/components/schemas/AlphaEvent" {
		t.Fatalf("alpha mapping = %v", mapping["alpha.delta"])
	}
	if mapping["beta.done"] != "#/components/schemas/BetaEvent" {
		t.Fatalf("beta mapping = %v", mapping["beta.done"])
	}
}

func TestRenameResponseObjectEnumsWhenSchemaPresent(t *testing.T) {
	dir := t.TempDir()
	spec := map[string]any{
		"openapi": "3.1.0",
		"info":    map[string]any{"title": "t", "version": "1"},
		"paths": map[string]any{
			"/embeddings": map[string]any{
				"post": map[string]any{
					"operationId": "createEmbedding",
					"responses": map[string]any{
						"200": map[string]any{
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{"$ref": "#/components/schemas/CreateEmbeddingResponse"},
								},
							},
						},
					},
				},
			},
		},
		"components": map[string]any{
			"schemas": map[string]any{
				"CreateEmbeddingResponse": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"object": map[string]any{"type": "string", "enum": []any{"list"}},
					},
				},
			},
		},
	}
	specPath := filepath.Join(dir, "openapi.json")
	writeJSON(t, specPath, spec)
	opsPath := filepath.Join(dir, "operations.txt")
	if err := os.WriteFile(opsPath, []byte("createEmbedding\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.json")
	if err := filterSpec(specPath, opsPath, out); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	obj := got["components"].(map[string]any)["schemas"].(map[string]any)["CreateEmbeddingResponse"].(map[string]any)["properties"].(map[string]any)["object"].(map[string]any)
	if obj["x-go-type-name"] != "EmbeddingObjectType" {
		t.Fatalf("x-go-type-name=%v", obj["x-go-type-name"])
	}
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

// SPDX-License-Identifier: AGPL-3.0-only

//go:build !corrupted_schema

package specvalidate

import (
	_ "embed"
	"strings"
)

//go:embed embed/validation_subspec.json
var embeddedSubspec []byte

//go:embed embed/relax-overlay.yaml
var embeddedOverlay []byte

//go:embed embed/schema-subset.yaml
var EmbeddedSchemaSubset []byte

//go:embed embed/meta.txt
var embeddedMeta string

func init() {
	embeddedSpecSHA, embeddedSubsetRetrieved = parseMeta(embeddedMeta)
}

func parseMeta(raw string) (sha, retrieved string) {
	for _, line := range strings.Split(raw, "\n") {
		key, val, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch key {
		case "spec_sha":
			sha = val
		case "schema_subset_retrieved":
			retrieved = val
		}
	}
	return sha, retrieved
}

var (
	embeddedSpecSHA         string
	embeddedSubsetRetrieved string
)

// EmbeddedOverlay returns the committed relax-overlay copy used at compile time.
func EmbeddedOverlay() []byte { return embeddedOverlay }

// EmbeddedSubspec returns the filtered OpenAPI sub-spec used for validation.
func EmbeddedSubspec() []byte { return embeddedSubspec }

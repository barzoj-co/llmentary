// SPDX-License-Identifier: AGPL-3.0-only

//go:build corrupted_schema

package specvalidate

// Deliberately invalid so NewServer / CompileEmbedded fails closed.
var embeddedSubspec = []byte(`{not-valid-json`)

var (
	embeddedOverlay         = []byte("overlay: 1.0.0\nentries: []\n")
	EmbeddedSchemaSubset    = []byte("retrieved: \"2026-10-18\"\nsupported:\n  keywords: []\n")
	embeddedSpecSHA         = "corrupted"
	embeddedSubsetRetrieved = "2026-10-18"
)

func EmbeddedOverlay() []byte { return embeddedOverlay }
func EmbeddedSubspec() []byte { return embeddedSubspec }

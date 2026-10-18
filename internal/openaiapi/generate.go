// SPDX-License-Identifier: AGPL-3.0-only

// Package openaiapi contains models and the strict server interface generated
// from a filtered subset of the official OpenAI OpenAPI spec.
//
// Regeneration is `make gen` (filter + oapi-codegen + SPDX + gofmt). The
// go:generate line below is a convenience alias for the same pipeline.
package openaiapi

//go:generate go run ../../tools/gencode

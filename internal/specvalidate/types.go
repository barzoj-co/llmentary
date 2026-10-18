// SPDX-License-Identifier: AGPL-3.0-only

// Package specvalidate validates raw Chat Completions and Responses request
// (and report-only response) bytes against the pinned OpenAI sub-spec.
package specvalidate

import "sync/atomic"

const (
	ModeOff     = "off"
	ModeWarn    = "warn"
	ModeEnforce = "enforce"

	ProfileStrict  = "strict"
	ProfileRelaxed = "relaxed"

	SideRequest  = "request"
	SideResponse = "response"

	SourceSyntax     = "syntax"
	SourceSchema     = "schema"
	SourceStrictMode = "strict_mode"

	MaxFindings     = 200
	MaxJSONDepth    = 128
	DefaultMaxBytes = 8 << 20
)

// CompileCount is incremented each time both profiles are compiled. Tests use
// it to prove --spec-validation=off does not compile anything.
var CompileCount atomic.Int64

// LastCompileDurationNS is the last successful compile duration.
var LastCompileDurationNS atomic.Int64

type Violation struct {
	Side       string `json:"side"`
	Source     string `json:"source"`
	Path       string `json:"path"`
	Pointer    string `json:"pointer"`
	Keyword    string `json:"keyword"`
	Message    string `json:"message"`
	Profile    string `json:"profile"`
	SchemaRef  string `json:"schema_ref,omitempty"`
	Offset     *int   `json:"offset"`
	EventIndex *int   `json:"event_index"`
	Count      int    `json:"count,omitempty"`
}

type Result struct {
	Violations    []Violation
	SkippedReason string
	DurationUS    int64
	Truncated     bool
	Checked       bool
}

type ClosedWorldReport struct {
	AlreadyClosed     []string `json:"already_closed"`
	ClosedByTransform []string `json:"closed_by_transform"`
	SkippedFreeForm   []string `json:"skipped_free_form"`
}

func intPtr(n int) *int { return &n }

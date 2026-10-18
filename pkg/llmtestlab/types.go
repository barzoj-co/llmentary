// SPDX-License-Identifier: MIT

package llmtestlab

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"
)

const (
	SessionHeaderName = "X-Test-Session"
	DefaultSessionID  = "default"
)

type Session struct {
	ID    string `json:"id"`
	Calls []Call `json:"calls"`
}

type Call struct {
	ID            string            `json:"id"`
	SessionID     string            `json:"session_id"`
	SessionSource string            `json:"session_source,omitempty"`
	Seq           int               `json:"seq"`
	StartedAt     time.Time         `json:"started_at"`
	FirstByteAt   *time.Time        `json:"first_byte_at,omitempty"`
	EndedAt       time.Time         `json:"ended_at"`
	LatencyMS     int64             `json:"latency_ms"`
	Provider      string            `json:"provider"`
	APIFamily     string            `json:"api_family"`
	Upstream      Upstream          `json:"upstream"`
	Request       Exchange          `json:"request"`
	Response      Exchange          `json:"response"`
	RequestIDs    map[string]string `json:"request_ids,omitempty"`
	RawRequest    []byte            `json:"raw_request,omitempty"`
	RawResponse   []byte            `json:"raw_response,omitempty"`
	SSEEvents     []SSEEvent        `json:"sse_events,omitempty"`
	Stream        bool              `json:"stream"`
	HTTPStatus    int               `json:"http_status"`
	RetryCount    *int              `json:"retry_count,omitempty"`
	RetryAfter    string            `json:"retry_after,omitempty"`
	Normalized    Normalized        `json:"normalized"`
}

type Upstream struct {
	Method     string `json:"method"`
	URL        string `json:"url"`
	APIVersion string `json:"api_version,omitempty"`
	Deployment string `json:"deployment,omitempty"`
}

type Exchange struct {
	Headers map[string][]string `json:"headers,omitempty"`
}

type SSEEvent struct {
	Event   string `json:"event,omitempty"`
	Data    []byte `json:"data,omitempty"`
	Comment string `json:"comment,omitempty"`
	AtMS    int64  `json:"at_ms"`
}

type Normalized struct {
	DerivedVersion     int             `json:"derived_version"`
	Model              Model           `json:"model"`
	ServedBy           string          `json:"served_by,omitempty"`
	SystemPromptHash   string          `json:"system_prompt_hash,omitempty"`
	SystemPrompt       string          `json:"system_prompt,omitempty"`
	MessagesCount      int             `json:"messages_count,omitempty"`
	PromptChars        int             `json:"prompt_chars,omitempty"`
	ContextServerSide  bool            `json:"context_server_side,omitempty"`
	PreviousResponseID string          `json:"previous_response_id,omitempty"`
	ToolsDeclared      []ToolDecl      `json:"tools_declared,omitempty"`
	ToolCalls          []ToolCall      `json:"tool_calls,omitempty"`
	ToolResultsIn      []ToolResult    `json:"tool_results_in,omitempty"`
	OutputText         string          `json:"output_text,omitempty"`
	Reasoning          *Reasoning      `json:"reasoning,omitempty"`
	FinishReason       string          `json:"finish_reason,omitempty"`
	NativeFinishReason string          `json:"native_finish_reason,omitempty"`
	Usage              *Usage          `json:"usage,omitempty"`
	ContentFilter      *ContentFilter  `json:"content_filter,omitempty"`
	Error              *CallError      `json:"error,omitempty"`
	StreamTruncated    bool            `json:"stream_truncated,omitempty"`
	ClientAborted      bool            `json:"client_aborted,omitempty"`
	ParserWarnings     []string        `json:"parser_warnings,omitempty"`
	Raw                json.RawMessage `json:"raw,omitempty"`
}

type Model struct {
	Requested string `json:"requested,omitempty"`
	Returned  string `json:"returned,omitempty"`
}

type ToolDecl struct {
	Name       string `json:"name,omitempty"`
	Type       string `json:"type,omitempty"`
	SchemaHash string `json:"schema_hash,omitempty"`
}

type ToolCall struct {
	Index          int             `json:"index"`
	ID             string          `json:"id,omitempty"`
	ItemID         string          `json:"item_id,omitempty"`
	Name           string          `json:"name"`
	Namespace      string          `json:"namespace,omitempty"`
	Kind           string          `json:"kind,omitempty"`
	ArgumentsRaw   string          `json:"arguments_raw,omitempty"`
	Arguments      json.RawMessage `json:"arguments,omitempty"`
	ArgsParseError string          `json:"args_parse_error,omitempty"`
}

type ToolResult struct {
	CallID     string `json:"call_id,omitempty"`
	Name       string `json:"name,omitempty"`
	OutputHash string `json:"output_hash,omitempty"`
}

type Reasoning struct {
	Present   bool `json:"present"`
	Chars     int  `json:"chars,omitempty"`
	Encrypted bool `json:"encrypted,omitempty"`
}

type Usage struct {
	InputTokens       *int            `json:"input_tokens,omitempty"`
	OutputTokens      *int            `json:"output_tokens,omitempty"`
	TotalTokens       *int            `json:"total_tokens,omitempty"`
	CachedInputTokens *int            `json:"cached_input_tokens,omitempty"`
	CacheWriteTokens  *int            `json:"cache_write_tokens,omitempty"`
	ReasoningTokens   *int            `json:"reasoning_tokens,omitempty"`
	CostUSD           *float64        `json:"cost_usd,omitempty"`
	Source            string          `json:"source,omitempty"`
	Raw               json.RawMessage `json:"raw,omitempty"`
}

type ContentFilter struct {
	PromptFiltered     bool                    `json:"prompt_filtered,omitempty"`
	CompletionFiltered bool                    `json:"completion_filtered,omitempty"`
	Categories         map[string]FilterResult `json:"categories,omitempty"`
	FilterSystemError  bool                    `json:"filter_system_error,omitempty"`
	Raw                []json.RawMessage       `json:"raw,omitempty"`
}

type FilterResult struct {
	Filtered bool   `json:"filtered,omitempty"`
	Severity string `json:"severity,omitempty"`
	Detected bool   `json:"detected,omitempty"`
}

type CallError struct {
	HTTPStatus int    `json:"http_status,omitempty"`
	Type       string `json:"type,omitempty"`
	Code       string `json:"code,omitempty"`
	Message    string `json:"message,omitempty"`
	Normalized string `json:"normalized,omitempty"`
	MidStream  bool   `json:"mid_stream,omitempty"`
}

func NewSessionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().UTC().Format("20060102150405.000000000")))
	}
	return hex.EncodeToString(b[:])
}

func Header(sessionID string) (string, string) {
	return SessionHeaderName, sessionID
}

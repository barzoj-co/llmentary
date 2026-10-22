// SPDX-License-Identifier: AGPL-3.0-only

// Package schemalint walks tool and output JSON Schemas against OpenAI's
// documented keyword subset and reports silently ignored request fields.
package schemalint

const (
	KindUnsupportedKeyword = "unsupported_schema_keyword"
	KindUnknownKeyword     = "unknown_schema_keyword"
	KindUnsupportedFormat  = "unsupported_format_value"
	KindSuspiciousEnum     = "suspicious_enum_values"
	KindUnknownRequest     = "unknown_request_field"
	KindVendorField        = "vendor_field"
	KindStrictFallbackPred = "strict_fallback_predicted"
	KindStrictFallback     = "strict_fallback"
	KindUnresolvedRef      = "unresolved_ref"

	SeverityWarning = "warning"
	SeverityInfo    = "info"

	ActionReject = "reject_in_enforce"
	ActionReport = "report_only"

	BehaviorIgnored  = "silently_ignored"
	BehaviorNotGuar  = "not_guaranteed"
	BehaviorFallback = "falls_back_to_non_strict"

	ContextChatToolParams      = "chat.tools.function.parameters"
	ContextChatFunctionsParams = "chat.functions.parameters"
	ContextResponsesToolParams = "responses.tools.parameters"
	ContextChatJSONSchema      = "chat.response_format.json_schema"
	ContextResponsesTextFormat = "responses.text.format"
	ContextChatMessage         = "chat.message"
	ContextChatToolFunction    = "chat.tools.function"
	ContextResponsesTools      = "responses.tools"
)

type Finding struct {
	Path                 string `json:"path"`
	Pointer              string `json:"pointer"`
	Key                  string `json:"key"`
	Kind                 string `json:"kind"`
	Context              string `json:"context"`
	EffectiveStrict      bool   `json:"effective_strict"`
	UpstreamBehavior     string `json:"upstream_behavior"`
	Evidence             string `json:"evidence"`
	Severity             string `json:"severity"`
	Action               string `json:"action"`
	Hint                 string `json:"hint"`
	SuggestedDescription string `json:"suggested_description,omitempty"`
	Confirmed            bool   `json:"confirmed"`
}

type RequestInfo struct {
	APIFamily string
	Model     string
	Body      map[string]any
}

type Linter struct {
	Subset Subset
}

func New(subset Subset) *Linter {
	return &Linter{Subset: subset}
}

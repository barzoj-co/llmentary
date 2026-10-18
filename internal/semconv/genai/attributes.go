// Package genai contains hand-written attribute constants for the
// OpenTelemetry GenAI semantic conventions repository at commit f49da22.
//
// The GenAI conventions have no tagged release yet and all attributes are
// development stability, so keep this file intentionally small and pinned.
package genai

const (
	OperationName              = "gen_ai.operation.name"
	ProviderName               = "gen_ai.provider.name"
	RequestModel               = "gen_ai.request.model"
	RequestStream              = "gen_ai.request.stream"
	RequestChoiceCount         = "gen_ai.request.choice.count"
	RequestSeed                = "gen_ai.request.seed"
	RequestMaxTokens           = "gen_ai.request.max_tokens"
	RequestTemperature         = "gen_ai.request.temperature"
	RequestTopP                = "gen_ai.request.top_p"
	RequestFrequencyPenalty    = "gen_ai.request.frequency_penalty"
	RequestPresencePenalty     = "gen_ai.request.presence_penalty"
	RequestStopSequences       = "gen_ai.request.stop_sequences"
	RequestReasoningLevel      = "gen_ai.request.reasoning.level"
	RequestPreviousResponseID  = "gen_ai.request.previous_response.id"
	ConversationID             = "gen_ai.conversation.id"
	OutputType                 = "gen_ai.output.type"
	ResponseID                 = "gen_ai.response.id"
	ResponseModel              = "gen_ai.response.model"
	ResponseFinishReasons      = "gen_ai.response.finish_reasons"
	ResponseTimeToFirstChunk   = "gen_ai.response.time_to_first_chunk"
	UsageInputTokens           = "gen_ai.usage.input_tokens"
	UsageOutputTokens          = "gen_ai.usage.output_tokens"
	UsageCacheReadInputTokens  = "gen_ai.usage.cache_read.input_tokens"
	UsageReasoningOutputTokens = "gen_ai.usage.reasoning.output_tokens"
	InputMessages              = "gen_ai.input.messages"
	OutputMessages             = "gen_ai.output.messages"
	SystemInstructions         = "gen_ai.system_instructions"
	ToolDefinitions            = "gen_ai.tool.definitions"
)

const (
	OpenAIAPIType             = "openai.api.type"
	OpenAIRequestServiceTier  = "openai.request.service_tier"
	OpenAIResponseServiceTier = "openai.response.service_tier"
	OpenAIResponseFingerprint = "openai.response.system_fingerprint"
)

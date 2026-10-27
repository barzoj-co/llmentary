# Assertions

Every assertion runs against the calls recorded for one session, after the
client has waited for the session to be quiet. Names follow each language's
conventions; the behaviour is the same unless a note says otherwise.

How to get an assertion object:

| | Python | TypeScript | Go |
|---|---|---|---|
| Fail on the first failed check | `llmentary.expect()` | `lm.expect()` | `lm.Expect(ctx)` (`t.Fatal`) |
| Collect failures, report at the end | `llmentary.expect(soft=True)` | `lm.softExpect()` | `lm.Check(ctx)` (`t.Error`) |
| Assert there were no calls at all | `llmentary.expect_no_calls()` | `lm.expectNoCalls()` | `lm.ExpectNoCalls(ctx)` |

Python assertions return the expectation, so they chain. TypeScript assertions
return a promise and must be awaited. Go assertions return `bool`.

## Calls

| Checks that | Python | TypeScript | Go |
|---|---|---|---|
| exactly *n* calls were made | `call_count(n)` | `callCount(n)` | `CallCount(n)` |
| between *min* and *max* calls were made | `call_count(min=1, max=4)` | | |
| a call used this model | `model("gpt-4.1-mini")` | `model("gpt-4.1-mini")` or a `RegExp` | `Model("gpt-4.1-mini")` |
| a request parameter has this value | `request_param("temperature", 0)` | `requestParam("temperature", 0)` or a predicate | `RequestParam("temperature", 0)` |
| the request was streamed | | `streamed()` | |

Call counts skip calls your client cancelled. Include them with
`include_cancelled=True` / `{ includeCancelled: true }` / `IncludeCancelled()`.
Go also has `ExcludeRetries()`.

## Tools

| Checks that | Python | TypeScript | Go |
|---|---|---|---|
| the model called a tool | `tool_called("get_order")` | `toolCalled("get_order")` | `ToolCalled("get_order")` |
| ... with these arguments (others allowed) | `args_subset("get_order", {"id": "A-1"})` | `toolCalled("get_order", { argsSubset: { id: "A-1" } })` | `ToolCalled("get_order", ArgsSubset(map[string]any{"id": "A-1"}))` |
| ... with arguments accepted by your function | `args_matcher("get_order", fn)` | `toolCalled("get_order", { argsMatcher: fn })` | `ToolCalled("get_order", ArgsMatch(fn))` |
| ... with one argument at a JSON path | | | `ToolCalled("get_order", ArgPath("filters.status", "open"))` |
| the model did not call a tool | `not_called("cancel_order")` | `notCalled("cancel_order")` | `ToolNotCalled("cancel_order")` |
| a tool was called *n* times | `called_times("get_order", 2)` | `calledTimes("get_order", 2)` | `ToolCalledTimes("get_order", 2)` |
| tools were called in this order | `order("a", "b")` | `order(["a", "b"])` | `ToolOrder("a", "b")` |
| tool *a* was called before tool *b* | | | `ToolBefore("a", "b")` |
| your app sent a tool's result back | `tool_result_fed_back("get_order")` | `toolResultFedBack("get_order")` | `ToolResultFedBack(callID)` |
| the next request contains that result | `tool_result_in_prompt("get_order")` | `toolResultInPrompt("get_order")` | `ToolResultInPrompt("get_order")` |

Notes:

- **Order.** Python `order()` and TypeScript `order()` pass when the names
  appear in that order, with other calls in between allowed. Python
  `order(..., strict=True)` and Go `ToolOrder` require exactly that sequence.
- **Fed back.** Go `ToolResultFedBack` takes a tool call id, not a tool name.
- **Parallel calls.** The three clients check different things:
  Python `parallel_tool_calls()` passes when some response contained more than
  one tool call; Go `ParallelToolCalls("a", "b")` passes when one response
  contained calls to all the named tools; TypeScript `parallelToolCalls()`
  checks the `parallel_tool_calls` request parameter.

## Prompts and responses

| Checks that | Python | TypeScript | Go |
|---|---|---|---|
| a request contains this text | `prompt_contains("…")` | `promptContains("…")` or a `RegExp` | `PromptContains("…")` |
| a request matches a regex | `prompt_regex(r"…")` | `promptContains(/…/)` | |
| the system prompt contains this text | | | `SystemPromptContains("…")` |
| a response contains this text | `response_contains("…")` | `responseContains("…")` or a `RegExp` | `ResponseContains("…")` |
| a response matches a regex | `response_regex(r"…")` | `responseContains(/…/)` | `ResponseMatches("…")` |
| the last answer mentions one of these | `final_response_mentions_any("a", "b")` | `finalResponseMentionsAny(["a", "b"])` | `FinalResponseMentionsAny("a", "b")` |
| the response is valid JSON | `response_json_valid()` | `responseJsonValid({ schema })` | `ResponseJSONValid()` |
| the response matches a JSON Schema | `response_json_schema(schema)` | `responseJsonSchema(schema)` | `ResponseJSONSchemaValid(schema)` |
| the finish reason is this | `finish_reason("stop")` | `finishReason("stop")` | `FinishReason("stop")` |

TypeScript also has `outputText()`, `outputMatchesDeltas()`,
`outputJsonValid(schema)`, `streamedToolArgsJson()` and `incomplete()` for
checking streamed output. Response text varies between runs; see
[Assert on model output](../how-to/model-output.md) before using these.

## Errors and retries

| Checks that | Python | TypeScript | Go |
|---|---|---|---|
| no call failed | `no_errors()` | `noErrors()` | `NoErrors()` |
| some call failed | `has_error()`, optionally a regex | `hasError()`, optionally a pattern | `HasError()` |
| a stream reported an error after it started | `mid_stream_error()` | `midStreamError()` | `MidStreamError()` |
| the error had this HTTP status, type or code | | `errorStatus(429)`, `errorType(…)`, `errorCode(…)` | |
| no request was retried | `no_retries()` | `noRetries()` | `NoRetries()` |
| the number of retries is in range | `retries(min_count=1, max_count=2)` | `retries({ min: 1, max: 2 })` | `Retries(n)` |

`no_errors()` fails only on calls with outcome `failed`. A call your client
cancelled is not an error; see [Call outcomes](../concepts.md#call-outcomes).
Go `Retries(n)` counts calls that were retries; Python and TypeScript count
retries summed over calls.

## Cancellation

| Checks that | Python | TypeScript | Go |
|---|---|---|---|
| some call was cancelled by the client | `aborted()`, optionally `phase="after_headers"` | `aborted()` | `Aborted()` |
| no call was cancelled | `no_cancelled()` | `noCancelled()` | `NoCancelled()` |

## Tokens and timing

| Checks that | Python | TypeScript | Go |
|---|---|---|---|
| total tokens are at most *n* | `tokens(max_total=n)` | `tokenBounds({ total: { max: n } })` | `TokensAtMost(n)` |
| total tokens are below *n* | | | `TokensBelow(n)` |
| prompt or completion tokens are in range | `tokens(min_prompt=…, max_completion=…)` | `tokenBounds({ prompt: {…}, completion: {…} })` | |
| cached or reasoning tokens are in range | | `usageDetails({ cachedTokens: { min: 1 } })` | |
| every call finished within a time | `latency(max_ms=2000)` | `latency({ max: 2000 })` | `LatencyBelow(2 * time.Second)` |
| the first token arrived within a time | `ttft(max_ms=500)` | `ttft({ max: 500 })` | `TTFTBelow(500 * time.Millisecond)` |

Token counts and timing vary. Use them as budgets with headroom, not as exact
values.

## Spec validation

| Checks that | Python | TypeScript | Go |
|---|---|---|---|
| no request or response broke the OpenAI schema | `no_spec_violations()` | `noSpecViolations()` | `NoSpecViolations()` |
| the provider did not silently ignore request fields | `no_ignored_fields()` | `noIgnoredFields()` | `NoIgnoredFields()` |

Both accept options to limit the check to one side (`request` or `response`),
to specific calls, or to skip paths and keywords. Both fail when the proxy did
not validate the call, unless you pass `allow_unvalidated=True` /
`AllowUnvalidated()`. See [Spec validation](../proxy.md#spec-validation).

## Isolation

| Checks that | Python | TypeScript | Go |
|---|---|---|---|
| this session never saw another session's messages | `isolated_from(other)` | `isolatedFrom(other)` | `IsolatedFrom(otherID)` |

Use this to prove that two concurrent conversations in your app do not leak
into each other. The rules are in the [isolation spec](../isolation-spec.md).

## Select calls

Scope assertions to part of the session.

| Selects calls | Python | TypeScript | Go |
|---|---|---|---|
| after a mark | `expect(since=mark)` | `expect({ since: mark })` | `Expect(ctx, Since(mark))` |
| that offered a tool | `query().offers_tool("x")` | `expect({ offersTool: "x" })` | `trace.Query().OffersTool("x")` |
| that offered no tools | `query().without_tools()` | `expect({ withoutTools: true })` | `trace.Query().WithoutTools()` |
| with a model or API type | `query(model=…, api_type=…)` | `expect({ model, apiType })` | `.Model(…)`, `.APIType(…)` |
| by outcome | `.committed()`, `.cancelled()`, `.failed()`, `.aborted()` | `query({}).committed()` etc. | `.Committed()`, `.Cancelled()`, `.Failed()`, `.Aborted()` |
| by HTTP status, errors or retries | | | `.Status(429)`, `.Errors()`, `.Retries()` |
| by position | | `expect({ index: 0 })` | `.Index(0)` |

In Go, `trace` comes from `lm.Calls(ctx)`; a query's `.Expect(t)` returns the
same assertions. If a selection matches no calls, `NoErrors` and
`ToolNotCalled` fail; see [Empty selections fail](../how-to/scope-a-turn.md#empty-selections-fail).

## Free-text helper

`answer_mentions_any(text, *candidates)` (Python), `mentionsAny(text, candidates)`
(TypeScript) and `MentionsAny(text, candidates...)` (Go) check any string, for
example an answer returned by your app's API. They ignore case and normalize
numbers.

## Deprecated

- `exclude_aborted=True` / `{ excludeAborted: true }` / `ExcludeAborted()` on
  call counts. Cancelled calls are excluded by default.
- `--llmentary-quiet-for` (pytest option). Use `--llmentary-wait-quiet`.

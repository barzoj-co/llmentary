// SPDX-License-Identifier: AGPL-3.0-only

package specvalidate

import (
	"strings"
	"testing"
)

func chatBody(extra string) []byte {
	if extra == "" {
		return []byte(`{"model":"m","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`)
	}
	return []byte(`{"model":"m","max_tokens":16,"messages":[{"role":"user","content":"hi"}],` + extra + `}`)
}

func TestCascadeMaxTokensNotBlamedForToolChoice(t *testing.T) {
	eng, err := CompileEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	body := chatBody(`"tool_choice":"any","tools":[{"type":"function","function":{"name":"x","parameters":{"type":"object"}}}]`)
	for _, profile := range []string{ProfileRelaxed, ProfileStrict} {
		res := eng.ValidateRequest(profile, "openai.chat", body, 0)
		if len(res.Violations) == 0 {
			t.Fatalf("%s: expected findings", profile)
		}
		first := res.Violations[0]
		if first.Pointer == "/max_tokens" || strings.Contains(first.Message, "max_tokens") {
			t.Fatalf("%s first=%+v all=%v", profile, first, pointers(res.Violations))
		}
		if !hasKeywordPointer(res.Violations, "enum", "/tool_choice") && !hasKeywordPointer(res.Violations, "const", "/tool_choice") {
			t.Fatalf("%s missing tool_choice enum: %v", profile, summarize(res.Violations))
		}
		if hasKeywordPointer(res.Violations, "unevaluatedProperties", "/max_tokens") {
			t.Fatalf("%s cascaded /max_tokens: %v", profile, summarize(res.Violations))
		}
	}
}

func TestCascadeMaxTokensNotBlamedForStrictMessageExtra(t *testing.T) {
	eng, err := CompileEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"model":"m","max_tokens":16,"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"x","foo":1}]}`)
	res := eng.ValidateRequest(ProfileStrict, "openai.chat", body, 0)
	if hasKeywordPointer(res.Violations, "unevaluatedProperties", "/max_tokens") {
		t.Fatalf("cascaded max_tokens: %v", summarize(res.Violations))
	}
	if !hasKeywordPointer(res.Violations, "unevaluatedProperties", "/messages/1/foo") {
		t.Fatalf("missing unknown key: %v", summarize(res.Violations))
	}
	roleEnum := 0
	requiredItem := 0
	for _, v := range res.Violations {
		if v.Pointer == "/messages/1/role" && v.Keyword == "enum" {
			roleEnum++
		}
		if v.Pointer == "/messages/1" && v.Keyword == "required" {
			requiredItem++
		}
	}
	if roleEnum+requiredItem > 0 {
		t.Fatalf("oneOf branch noise kept: %v", summarize(res.Violations))
	}
	if res.Violations[0].Pointer != "/messages/1/foo" {
		t.Fatalf("first=%+v want /messages/1/foo", res.Violations[0])
	}
}

func TestCascadeUnknownKeyEachMessageRole(t *testing.T) {
	eng, err := CompileEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	bodies := map[string][]byte{
		"system":    []byte(`{"model":"m","messages":[{"role":"system","content":"s","foo":1}]}`),
		"developer": []byte(`{"model":"m","messages":[{"role":"developer","content":"d","foo":1}]}`),
		"user":      []byte(`{"model":"m","messages":[{"role":"user","content":"u","foo":1}]}`),
		"assistant": []byte(`{"model":"m","messages":[{"role":"assistant","content":"a","foo":1}]}`),
		"tool":      []byte(`{"model":"m","messages":[{"role":"tool","content":"t","tool_call_id":"c1","foo":1}]}`),
		"function":  []byte(`{"model":"m","messages":[{"role":"function","content":"f","name":"n","foo":1}]}`),
	}
	for role, body := range bodies {
		res := eng.ValidateRequest(ProfileStrict, "openai.chat", body, 0)
		if !hasKeywordPointer(res.Violations, "unevaluatedProperties", "/messages/0/foo") {
			t.Fatalf("%s missing /messages/0/foo: %v", role, summarize(res.Violations))
		}
		if hasKeywordPointer(res.Violations, "unevaluatedProperties", "/max_tokens") {
			t.Fatalf("%s unexpected max_tokens", role)
		}
	}
}

func TestCascadeUnknownKeyInToolCallsAndContentPart(t *testing.T) {
	eng, err := CompileEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	tool := []byte(`{"model":"m","messages":[{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"x","arguments":"{}"},"foo":1}]}]}`)
	res := eng.ValidateRequest(ProfileStrict, "openai.chat", tool, 0)
	if !hasKeywordPointer(res.Violations, "unevaluatedProperties", "/messages/0/tool_calls/0/foo") {
		t.Fatalf("tool_calls extra: %v", summarize(res.Violations))
	}
	part := []byte(`{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"hi","foo":1}]}]}`)
	res = eng.ValidateRequest(ProfileStrict, "openai.chat", part, 0)
	if !hasKeywordPointer(res.Violations, "unevaluatedProperties", "/messages/0/content/0/foo") {
		t.Fatalf("content part extra: %v", summarize(res.Violations))
	}
}

func TestCascadeTwoIndependentErrors(t *testing.T) {
	eng, err := CompileEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"model":"m","max_tokens":16,"foo":1,"messages":[{"role":"user","content":"hi"}],"tool_choice":"any","tools":[{"type":"function","function":{"name":"x","parameters":{"type":"object"}}}]}`)
	res := eng.ValidateRequest(ProfileRelaxed, "openai.chat", body, 0)
	if hasKeywordPointer(res.Violations, "unevaluatedProperties", "/max_tokens") {
		t.Fatalf("valid field blamed: %v", summarize(res.Violations))
	}
	if !hasKeywordPointer(res.Violations, "unevaluatedProperties", "/foo") {
		t.Fatalf("missing /foo: %v", summarize(res.Violations))
	}
	if !hasKeywordPointer(res.Violations, "enum", "/tool_choice") && !hasKeywordPointer(res.Violations, "const", "/tool_choice") {
		t.Fatalf("missing tool_choice: %v", summarize(res.Violations))
	}
}

func TestCascadeValidRequestZeroFindings(t *testing.T) {
	eng, err := CompileEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"model":"m","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`)
	for _, profile := range []string{ProfileRelaxed, ProfileStrict} {
		res := eng.ValidateRequest(profile, "openai.chat", body, 0)
		if n := countSchemaFindings(res.Violations); n != 0 {
			t.Fatalf("%s findings=%d %v", profile, n, summarize(res.Violations))
		}
	}
}

func TestCascadeResponseFormatBadType(t *testing.T) {
	eng, err := CompileEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	body := chatBody(`"response_format":{"type":"regex"}`)
	res := eng.ValidateRequest(ProfileRelaxed, "openai.chat", body, 0)
	if hasKeywordPointer(res.Violations, "unevaluatedProperties", "/max_tokens") {
		t.Fatalf("cascaded max_tokens: %v", summarize(res.Violations))
	}
	if !hasPointerPrefix(res.Violations, "/response_format") {
		t.Fatalf("missing response_format: %v", summarize(res.Violations))
	}
	if res.Violations[0].Pointer == "/max_tokens" {
		t.Fatalf("first still max_tokens: %+v", res.Violations[0])
	}
}

func TestCascadeResponsesInputUnknownAndMissingStrict(t *testing.T) {
	eng, err := CompileEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	unknown := []byte(`{"model":"m","input":[{"type":"message","role":"user","content":"hi","foo":1}]}`)
	res := eng.ValidateRequest(ProfileStrict, "openai.responses", unknown, 0)
	if !hasKeywordPointer(res.Violations, "unevaluatedProperties", "/input/0/foo") && !hasPointerPrefix(res.Violations, "/input/0") {
		t.Fatalf("missing input item finding: %v", summarize(res.Violations))
	}
	if n := len(res.Violations); n > 10 {
		t.Fatalf("input unknown still cascaded: n=%d %v", n, summarize(res.Violations))
	}

	noStrict := []byte(`{"model":"m","input":"hi","tools":[{"type":"function","name":"x","parameters":{"type":"object","properties":{"a":{"type":"string"}}}}]}`)
	res = eng.ValidateRequest(ProfileStrict, "openai.responses", noStrict, 0)
	if n := len(res.Violations); n > 4 {
		t.Fatalf("missing strict cascaded n=%d %v", n, summarize(res.Violations))
	}
	if !hasKeywordPointer(res.Violations, "required", "/tools/0") {
		t.Fatalf("missing required /tools/0: %v", summarize(res.Violations))
	}
	if res.Violations[0].Keyword != "required" || res.Violations[0].Pointer != "/tools/0" {
		t.Fatalf("first=%+v want required /tools/0", res.Violations[0])
	}
	if !strings.Contains(res.Violations[0].Message, "strict") {
		t.Fatalf("first message=%q want missing strict", res.Violations[0].Message)
	}
	if hasKeywordPointer(res.Violations, "unevaluatedProperties", "/tools/0/parameters/properties") ||
		hasKeywordPointer(res.Violations, "unevaluatedProperties", "/tools/0/parameters/type") ||
		hasKeywordPointer(res.Violations, "type", "/tools/0/parameters") {
		t.Fatalf("losing-branch parameters kept: %v", summarize(res.Violations))
	}
}

func TestCollectKnownPropertiesHasMaxTokens(t *testing.T) {
	eng, err := CompileEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	known := eng.knownReq[ProfileRelaxed+"|openai.chat"]
	if !known.has("", "max_tokens") || !known.has("", "messages") || !known.has("", "tool_choice") {
		t.Fatalf("root props missing: %v", known[""])
	}
	if !known.has("/messages/*", "role") || !known.has("/messages/*", "content") {
		t.Fatalf("message props missing: %v", known["/messages/*"])
	}
	if known.has("", "foo") {
		t.Fatal("unknown root field listed")
	}
}

func TestGeneralizeAndSplitPointer(t *testing.T) {
	t.Parallel()
	parent, field := splitPointerField("/max_tokens")
	if parent != "" || field != "max_tokens" {
		t.Fatalf("%q %q", parent, field)
	}
	parent, field = splitPointerField("/messages/1/foo")
	if parent != "/messages/1" || field != "foo" {
		t.Fatalf("%q %q", parent, field)
	}
	if got := generalizePointer("/messages/1"); got != "/messages/*" {
		t.Fatalf("gen=%q", got)
	}
	if got := arrayItemPrefix("/messages/1/foo"); got != "/messages/1" {
		t.Fatalf("item=%q", got)
	}
}

func hasKeywordPointer(vs []Violation, keyword, pointer string) bool {
	for _, v := range vs {
		if v.Keyword == keyword && v.Pointer == pointer {
			return true
		}
	}
	return false
}

func hasPointerPrefix(vs []Violation, prefix string) bool {
	for _, v := range vs {
		if v.Pointer == prefix || strings.HasPrefix(v.Pointer, prefix+"/") {
			return true
		}
	}
	return false
}

func TestCascadeHelperEdges(t *testing.T) {
	t.Parallel()
	if collectKnownProperties(nil, "") != nil {
		t.Fatal("empty root should skip walk")
	}
	if collectKnownProperties(map[string]any{"components": map[string]any{"schemas": map[string]any{}}}, "Missing") == nil {
		t.Fatal("missing schema should still return a map")
	}
	if joinPattern("", "x") != "/x" {
		t.Fatalf("join empty parent=%q", joinPattern("", "x"))
	}
	var none objectProps
	if none.has("", "model") {
		t.Fatal("nil objectProps.has")
	}
	if parent, field := splitPointerField(""); field != "" || parent != "" {
		t.Fatalf("empty pointer parent=%q field=%q", parent, field)
	}
	if underPointer("/a", "") {
		t.Fatal("empty root is not a prefix")
	}
	if isOneOfBranchNoise(schemaErr{pointer: "/tools/0", keyword: "required"}, "", nil) {
		t.Fatal("empty item prefix is not noise")
	}
	if isOneOfBranchNoise(schemaErr{pointer: "/tools/0/role", keyword: "enum"}, "/tools/0", nil) != true {
		t.Fatal("role enum under item should be noise")
	}
	if isOneOfBranchNoise(schemaErr{pointer: "/tools/0/name/nested", keyword: "type"}, "/tools/0", nil) {
		t.Fatal("nested type is not item-level noise")
	}
	if got := filterCascadedFindings(nil, objectProps{}); got != nil && len(got) != 0 {
		t.Fatalf("empty in=%v", got)
	}
	known := objectProps{"": {"model": {}, "max_tokens": {}}}
	dropped := filterCascadedFindings([]schemaErr{{
		keyword:    "unevaluatedProperties",
		pointer:    "/",
		extraNames: []string{"model", "max_tokens"},
	}}, known)
	if len(dropped) != 0 {
		t.Fatalf("root known extras should drop: %v", dropped)
	}
	kept := filterCascadedFindings([]schemaErr{{
		keyword:    "unevaluatedProperties",
		pointer:    "/",
		extraNames: []string{"model", "vendor_x"},
	}}, known)
	if len(kept) != 1 || len(kept[0].extraNames) != 1 || kept[0].extraNames[0] != "vendor_x" {
		t.Fatalf("kept extras=%v", kept)
	}
	if keywordSpecificity("pattern") != 2 || keywordSpecificity("banana") != 1 {
		t.Fatalf("specificity pattern=%d banana=%d", keywordSpecificity("pattern"), keywordSpecificity("banana"))
	}
	if !violationMoreSpecific(Violation{Side: "request"}, Violation{Side: "response"}) {
		t.Fatal("request side should rank before response")
	}
	spec := map[string]any{
		"components": map[string]any{
			"schemas": map[string]any{
				"Root": map[string]any{
					"prefixItems": []any{
						map[string]any{"properties": map[string]any{"a": map[string]any{"type": "string"}}},
					},
				},
			},
		},
	}
	props := collectKnownProperties(spec, "Root")
	if !props.has("/*", "a") {
		t.Fatalf("prefixItems props=%v", props)
	}
}

func countSchemaFindings(vs []Violation) int {
	n := 0
	for _, v := range vs {
		if v.Keyword == "truncated" {
			continue
		}
		n++
	}
	return n
}

func pointers(vs []Violation) []string {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		out = append(out, v.Pointer)
	}
	return out
}

func summarize(vs []Violation) []string {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		out = append(out, v.Pointer+" "+v.Keyword)
	}
	return out
}

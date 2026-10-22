// SPDX-License-Identifier: AGPL-3.0-only

package specvalidate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/dlclark/regexp2"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	schemaURL    = "https://llmentary.invalid/openai-subspec"
	chatRequest  = "CreateChatCompletionRequest"
	chatResponse = "CreateChatCompletionResponse"
	chatStream   = "CreateChatCompletionStreamResponse"
	respRequest  = "CreateResponse"
	respResponse = "Response"
	respStream   = "ResponseStreamEvent"
)

type Engine struct {
	Overlay         Overlay
	SpecSHA         string
	SubsetRetrieved string
	Report          ClosedWorldReport

	strictChatReq  *jsonschema.Schema
	strictRespReq  *jsonschema.Schema
	strictChatResp *jsonschema.Schema
	strictChatSSE  *jsonschema.Schema
	strictRespResp *jsonschema.Schema
	strictRespSSE  *jsonschema.Schema

	relaxedChatReq  *jsonschema.Schema
	relaxedRespReq  *jsonschema.Schema
	relaxedChatResp *jsonschema.Schema
	relaxedChatSSE  *jsonschema.Schema
	relaxedRespResp *jsonschema.Schema
	relaxedRespSSE  *jsonschema.Schema

	// knownReq is profile|apiFamily → declared properties on request objects.
	knownReq map[string]objectProps

	overlay Overlay
}

func Compile(subspec []byte, overlayRaw []byte, specSHA, subsetRetrieved string) (*Engine, error) {
	start := time.Now()
	ov, err := ParseOverlay(overlayRaw)
	if err != nil {
		return nil, err
	}
	base, err := decodeSpec(subspec)
	if err != nil {
		return nil, err
	}
	strictDoc := cloneMap(base)
	applyBothProfileCorrections(strictDoc)
	report := ApplyClosedWorld(strictDoc)
	relaxedDoc := cloneMap(strictDoc)
	if err := ApplyOverlay(relaxedDoc, ov); err != nil {
		return nil, err
	}

	e := &Engine{
		Overlay:         ov,
		overlay:         ov,
		SpecSHA:         specSHA,
		SubsetRetrieved: subsetRetrieved,
		Report:          report,
	}
	if err := e.compileProfile(strictDoc, ProfileStrict); err != nil {
		return nil, fmt.Errorf("compile strict profile: %w", err)
	}
	if err := e.compileProfile(relaxedDoc, ProfileRelaxed); err != nil {
		return nil, fmt.Errorf("compile relaxed profile: %w", err)
	}
	CompileCount.Add(1)
	LastCompileDurationNS.Store(time.Since(start).Nanoseconds())
	return e, nil
}

func CompileEmbedded() (*Engine, error) {
	if len(embeddedSubspec) == 0 {
		return nil, fmt.Errorf("embedded validation sub-spec is empty")
	}
	if !json.Valid(embeddedSubspec) {
		return nil, fmt.Errorf("embedded validation sub-spec is not valid JSON")
	}
	return Compile(embeddedSubspec, embeddedOverlay, embeddedSpecSHA, embeddedSubsetRetrieved)
}

func (e *Engine) compileProfile(spec map[string]any, profile string) error {
	js := openAPIToJSONSchema(spec)
	raw, err := json.Marshal(js)
	if err != nil {
		return err
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.UseRegexpEngine(compileRegexp2)
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("unmarshal schema doc: %w", err)
	}
	if err := compiler.AddResource(schemaURL, doc); err != nil {
		return fmt.Errorf("add schema resource: %w", err)
	}
	compile := func(name string) (*jsonschema.Schema, error) {
		sch, err := compiler.Compile(schemaURL + "#/$defs/" + name)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		return sch, nil
	}
	chatReq, err := compile(chatRequest)
	if err != nil {
		return err
	}
	respReq, err := compile(respRequest)
	if err != nil {
		return err
	}
	chatResp, err := compile(chatResponse)
	if err != nil {
		return err
	}
	chatSSE, err := compile(chatStream)
	if err != nil {
		return err
	}
	respResp, err := compile(respResponse)
	if err != nil {
		return err
	}
	respSSE, err := compile(respStream)
	if err != nil {
		return err
	}
	if e.knownReq == nil {
		e.knownReq = map[string]objectProps{}
	}
	e.knownReq[profile+"|openai.chat"] = collectKnownProperties(spec, chatRequest)
	e.knownReq[profile+"|openai.responses"] = collectKnownProperties(spec, respRequest)
	switch profile {
	case ProfileStrict:
		e.strictChatReq, e.strictRespReq = chatReq, respReq
		e.strictChatResp, e.strictChatSSE = chatResp, chatSSE
		e.strictRespResp, e.strictRespSSE = respResp, respSSE
	case ProfileRelaxed:
		e.relaxedChatReq, e.relaxedRespReq = chatReq, respReq
		e.relaxedChatResp, e.relaxedChatSSE = chatResp, chatSSE
		e.relaxedRespResp, e.relaxedRespSSE = respResp, respSSE
	}
	return nil
}

func decodeSpec(raw []byte) (map[string]any, error) {
	var spec map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&spec); err != nil {
		return nil, fmt.Errorf("parse validation sub-spec: %w", err)
	}
	return spec, nil
}

func openAPIToJSONSchema(spec map[string]any) map[string]any {
	schemas := cloneMap(componentSchemas(spec))
	rewriteRefs(schemas)
	return map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id":     schemaURL,
		"$defs":   schemas,
	}
}

func rewriteRefs(node any) {
	switch n := node.(type) {
	case map[string]any:
		if ref, ok := n["$ref"].(string); ok {
			if strings.HasPrefix(ref, "#/components/schemas/") {
				n["$ref"] = "#/$defs/" + strings.TrimPrefix(ref, "#/components/schemas/")
			}
		}
		for _, v := range n {
			rewriteRefs(v)
		}
	case []any:
		for _, v := range n {
			rewriteRefs(v)
		}
	}
}

func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	raw, _ := json.Marshal(in)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}

type regexp2RE regexp2.Regexp

func (r *regexp2RE) MatchString(s string) bool {
	ok, err := (*regexp2.Regexp)(r).MatchString(s)
	return err == nil && ok
}

func (r *regexp2RE) String() string { return (*regexp2.Regexp)(r).String() }

func compileRegexp2(expr string) (jsonschema.Regexp, error) {
	re, err := regexp2.Compile(expr, regexp2.ECMAScript)
	if err != nil {
		return nil, err
	}
	return (*regexp2RE)(re), nil
}

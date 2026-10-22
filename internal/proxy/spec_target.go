// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/barzoj-co/llmentary/internal/schemalint"
	"github.com/barzoj-co/llmentary/internal/specvalidate"
)

const (
	SpecTargetOpenAI     = "openai"
	SpecTargetCompatible = "openai-compatible"
	vendorFieldHint      = "Forwarded to the upstream unchanged. This field is not in the OpenAI request schema."
	vendorValueHint      = "Forwarded to the upstream unchanged. OpenAI rejects this enum value; openai-compatible providers often accept it."
	unknownRequestHintOA = "The live OpenAI API accepts this field and silently ignores it. Remove it, or it will never reach the model."
)

func defaultSpecTarget(u *url.URL) string {
	if u == nil {
		return SpecTargetCompatible
	}
	host := strings.ToLower(u.Hostname())
	switch {
	case host == "api.openai.com":
		return SpecTargetOpenAI
	case strings.HasSuffix(host, ".openai.azure.com"),
		strings.HasSuffix(host, ".cognitiveservices.azure.com"),
		strings.HasSuffix(host, ".services.ai.azure.com"):
		return SpecTargetOpenAI
	default:
		return SpecTargetCompatible
	}
}

func validateSpecTargetValue(target string) error {
	switch target {
	case SpecTargetOpenAI, SpecTargetCompatible:
		return nil
	default:
		return fmt.Errorf("--spec-target=%q is invalid; allowed values: openai, openai-compatible (env LLMENTARY_SPEC_TARGETS)", target)
	}
}

func (c *Config) AddSpecTarget(spec string) error {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil
	}
	name, target, ok := strings.Cut(spec, "=")
	if !ok {
		return fmt.Errorf("--spec-target=%q is invalid; expected name=openai or name=openai-compatible (env LLMENTARY_SPEC_TARGETS)", spec)
	}
	name = strings.Trim(strings.TrimSpace(name), "/")
	target = strings.TrimSpace(target)
	if name == "" {
		return fmt.Errorf("spec-target upstream name cannot be empty")
	}
	if err := validateSpecTargetValue(target); err != nil {
		return err
	}
	if c.SpecTargets == nil {
		c.SpecTargets = map[string]string{}
	}
	c.SpecTargets[name] = target
	return nil
}

func (c Config) specTargetFor(name string, u *url.URL) string {
	if c.SpecTargets != nil {
		if t, ok := c.SpecTargets[name]; ok {
			return t
		}
	}
	return defaultSpecTarget(u)
}

func (c Config) resolvedSpecTargets() map[string]string {
	out := map[string]string{}
	for name, u := range c.Upstreams {
		out[name] = c.specTargetFor(name, u)
	}
	return out
}

func (c *Config) validateSpecTargets() error {
	if c == nil {
		return nil
	}
	for name, target := range c.SpecTargets {
		if err := validateSpecTargetValue(target); err != nil {
			return err
		}
		if _, ok := c.Upstreams[name]; !ok {
			return fmt.Errorf("--spec-target unknown upstream %q (env LLMENTARY_SPEC_TARGETS)", name)
		}
	}
	return nil
}

func applySpecTarget(target string, violations []specvalidate.Violation, ignored []schemalint.Finding) ([]specvalidate.Violation, []schemalint.Finding) {
	if target == SpecTargetCompatible {
		return applyCompatibleTarget(violations, ignored)
	}
	return applyOpenAITarget(violations, ignored)
}

func applyOpenAITarget(violations []specvalidate.Violation, ignored []schemalint.Finding) ([]specvalidate.Violation, []schemalint.Finding) {
	for i := range ignored {
		if ignored[i].Kind == schemalint.KindUnknownRequest && ignored[i].Hint == "" {
			ignored[i].Hint = unknownRequestHintOA
		}
	}
	return violations, ignored
}

func applyCompatibleTarget(violations []specvalidate.Violation, ignored []schemalint.Finding) ([]specvalidate.Violation, []schemalint.Finding) {
	kept := violations[:0]
	for _, v := range violations {
		if v.Side == specvalidate.SideResponse {
			kept = append(kept, v)
			continue
		}
		if isVendorPropertyViolation(v) {
			ignored = append(ignored, vendorFindingFromViolation(v, schemalint.KindVendorField, schemalint.SeverityInfo, vendorFieldHint))
			continue
		}
		if !isCompatibleReject(v) {
			v.Severity = specvalidate.SeverityWarning
			kept = append(kept, v)
			continue
		}
		kept = append(kept, v)
	}
	promoteExhaustedItemRoleEnums(kept)
	for i := range ignored {
		if ignored[i].Kind != schemalint.KindUnknownRequest {
			continue
		}
		ignored[i].Kind = schemalint.KindVendorField
		ignored[i].Severity = schemalint.SeverityInfo
		ignored[i].Action = schemalint.ActionReport
		ignored[i].UpstreamBehavior = "forwarded"
		ignored[i].Hint = vendorFieldHint
	}
	return kept, ignored
}

func isVendorPropertyViolation(v specvalidate.Violation) bool {
	switch v.Keyword {
	case "unevaluatedProperties", "additionalProperties":
		return true
	default:
		return false
	}
}

// isCompatibleReject keeps known-field type/required/enum/const checks
// rejecting in enforce mode. Composition leftovers from extra vendor
// fields (item-level required, losing-branch type/enum, vendor-union
// objects) and vendor-extended enums must not 400 openai-compatible traffic.
func isCompatibleReject(v specvalidate.Violation) bool {
	if v.Side == specvalidate.SideResponse {
		return false
	}
	if v.Source == specvalidate.SourceSyntax {
		return true
	}
	switch v.Keyword {
	case "trailingData", "duplicateKey", "utf8BOM", "numberRange":
		return true
	case "type", "required":
		return !isCompatibleCompositionLeftover(v)
	case "enum", "const":
		return !isExtendedEnumViolation(v) && !isItemDiscriminatorViolation(v)
	}
	return false
}

func isCompatibleCompositionLeftover(v specvalidate.Violation) bool {
	switch v.Keyword {
	case "type":
		return !isKnownFieldTypeViolation(v)
	case "required":
		return !isKnownFieldRequiredViolation(v)
	default:
		return false
	}
}

func isKnownFieldTypeViolation(v specvalidate.Violation) bool {
	switch v.Pointer {
	case "/max_tokens", "/max_completion_tokens", "/temperature", "/top_p",
		"/n", "/stream", "/presence_penalty", "/frequency_penalty", "/seed",
		"/model", "/messages", "/tools":
		return true
	}
	segs := pointerSegments(v.Pointer)
	if len(segs) == 3 && segs[0] == "messages" && isPointerIndex(segs[1]) {
		switch segs[2] {
		case "role":
			return true
		case "content":
			return isIncompatibleMessageContentType(v.Message)
		}
	}
	return false
}

func isIncompatibleMessageContentType(message string) bool {
	got := schemaTypeGot(message)
	switch got {
	case "null", "string", "array":
		return false
	default:
		return got != ""
	}
}

func schemaTypeGot(message string) string {
	const prefix = "got "
	i := strings.Index(message, prefix)
	if i < 0 {
		return ""
	}
	rest := message[i+len(prefix):]
	if j := strings.Index(rest, ","); j >= 0 {
		rest = rest[:j]
	}
	return strings.TrimSpace(rest)
}

func isKnownFieldRequiredViolation(v specvalidate.Violation) bool {
	if vendorUnionPointer(v.Pointer) {
		return false
	}
	if nestedUnderIndexedItem(v.Pointer) {
		return false
	}
	return requiredMentionsCoreField(v.Message, v.Pointer)
}

func requiredMentionsCoreField(message, pointer string) bool {
	if chatMessagePointer(pointer) {
		if strings.Contains(message, "'tool_call_id'") || strings.Contains(message, "'name'") {
			return false
		}
		return strings.Contains(message, "'content'") || strings.Contains(message, "'role'")
	}
	if pointer == "" || pointer == "/" {
		return strings.Contains(message, "'messages'") || strings.Contains(message, "'model'")
	}
	return false
}

func chatMessagePointer(pointer string) bool {
	segs := pointerSegments(pointer)
	return len(segs) == 2 && segs[0] == "messages" && isPointerIndex(segs[1])
}

func nestedUnderIndexedItem(pointer string) bool {
	segs := pointerSegments(pointer)
	if len(segs) < 3 {
		return false
	}
	for _, seg := range segs {
		if isPointerIndex(seg) {
			return true
		}
	}
	return false
}

func vendorUnionPointer(pointer string) bool {
	switch pointer {
	case "/response_format", "/tool_choice", "/stream_options", "/reasoning", "/thinking":
		return true
	}
	return false
}

func isItemDiscriminatorViolation(v specvalidate.Violation) bool {
	if v.Keyword != "enum" && v.Keyword != "const" {
		return false
	}
	last := lastPathSegment(v.Pointer)
	if last != "role" && last != "type" {
		return false
	}
	return pointerHasArrayIndex(v.Pointer)
}

var chatRoleEnumValues = []string{"developer", "system", "user", "assistant", "tool", "function"}

func promoteExhaustedItemRoleEnums(vs []specvalidate.Violation) {
	seen := map[string]map[string]struct{}{}
	for _, v := range vs {
		if (v.Keyword != "enum" && v.Keyword != "const") || lastPathSegment(v.Pointer) != "role" || !pointerHasArrayIndex(v.Pointer) {
			continue
		}
		if seen[v.Pointer] == nil {
			seen[v.Pointer] = map[string]struct{}{}
		}
		for _, role := range chatRoleEnumValues {
			if strings.Contains(v.Message, "'"+role+"'") {
				seen[v.Pointer][role] = struct{}{}
			}
		}
	}
	exhausted := map[string]bool{}
	for ptr, roles := range seen {
		if len(roles) == len(chatRoleEnumValues) {
			exhausted[ptr] = true
		}
	}
	if len(exhausted) == 0 {
		return
	}
	for i := range vs {
		if exhausted[vs[i].Pointer] && (vs[i].Keyword == "enum" || vs[i].Keyword == "const") {
			vs[i].Severity = ""
		}
	}
}

func pointerSegments(pointer string) []string {
	p := strings.Trim(pointer, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

func isPointerIndex(seg string) bool {
	if seg == "" {
		return false
	}
	for _, c := range seg {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func pointerHasArrayIndex(pointer string) bool {
	for _, seg := range pointerSegments(pointer) {
		if isPointerIndex(seg) {
			return true
		}
	}
	return false
}

func isExtendedEnumViolation(v specvalidate.Violation) bool {
	if v.Keyword != "enum" && v.Keyword != "const" {
		return false
	}
	switch v.Pointer {
	case "/tool_choice", "/response_format/type", "/service_tier", "/finish_reason":
		return true
	}
	for _, suffix := range []string{"/tool_choice", "/response_format/type", "/service_tier", "/finish_reason"} {
		if strings.HasSuffix(v.Pointer, suffix) {
			return true
		}
	}
	return false
}

func vendorFindingFromViolation(v specvalidate.Violation, kind, severity, hint string) schemalint.Finding {
	key := lastPathSegment(v.Pointer)
	return schemalint.Finding{
		Path:             v.Path,
		Pointer:          v.Pointer,
		Key:              key,
		Kind:             kind,
		Context:          "request",
		UpstreamBehavior: "forwarded",
		Severity:         severity,
		Action:           schemalint.ActionReport,
		Hint:             hint,
	}
}

func lastPathSegment(pointer string) string {
	pointer = strings.Trim(pointer, "/")
	if pointer == "" {
		return ""
	}
	if i := strings.LastIndex(pointer, "/"); i >= 0 {
		return pointer[i+1:]
	}
	return pointer
}

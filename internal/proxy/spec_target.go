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

// isCompatibleReject keeps OpenAI-shaped type/required checks on core
// request fields and every syntax/precheck finding. Composition leftovers
// from extra vendor fields (item-level required, losing-branch type, …)
// must not 400 openai-compatible traffic.
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
		switch v.Pointer {
		case "", "/", "/messages", "/max_tokens", "/temperature", "/top_p", "/n", "/stream", "/model", "/input", "/tools":
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

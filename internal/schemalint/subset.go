// SPDX-License-Identifier: AGPL-3.0-only

package schemalint

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

type Subset struct {
	Retrieved string `yaml:"retrieved"`
	Sources   struct {
		StructuredOutputs     string `yaml:"structured_outputs"`
		FunctionCallingStrict string `yaml:"function_calling_strict"`
	} `yaml:"sources"`
	Supported struct {
		Types        []string `yaml:"types"`
		Keywords     []string `yaml:"keywords"`
		FormatValues []string `yaml:"format_values"`
	} `yaml:"supported"`
	Unverified struct {
		Keywords []string `yaml:"keywords"`
	} `yaml:"unverified"`
	UnsupportedDocumented struct {
		Keywords            []string `yaml:"keywords"`
		FineTunedAdditional []string `yaml:"fine_tuned_additional"`
	} `yaml:"unsupported_documented"`
	Limits struct {
		MaxProperties                    int `yaml:"max_properties"`
		MaxNesting                       int `yaml:"max_nesting"`
		MaxEnumValues                    int `yaml:"max_enum_values"`
		MaxNameChars                     int `yaml:"max_name_chars"`
		MaxStringEnumValuesBeforeCharCap int `yaml:"max_string_enum_values_before_char_cap"`
		MaxStringEnumChars               int `yaml:"max_string_enum_chars"`
	} `yaml:"limits"`
	Hints map[string]string `yaml:"hints"`

	supportedSet   map[string]struct{}
	unverifiedSet  map[string]struct{}
	unsupportedSet map[string]struct{}
	formatSet      map[string]struct{}
	ftExtraSet     map[string]struct{}
}

func LoadSubset(raw []byte) (Subset, error) {
	var s Subset
	if err := yaml.Unmarshal(raw, &s); err != nil {
		return Subset{}, fmt.Errorf("schema-subset.yaml: %w", err)
	}
	if s.Retrieved == "" {
		return Subset{}, fmt.Errorf("schema-subset.yaml: missing retrieved date")
	}
	s.supportedSet = setOf(s.Supported.Keywords)
	s.unverifiedSet = setOf(s.Unverified.Keywords)
	s.unsupportedSet = setOf(s.UnsupportedDocumented.Keywords)
	s.formatSet = setOf(s.Supported.FormatValues)
	s.ftExtraSet = setOf(s.UnsupportedDocumented.FineTunedAdditional)
	return s, nil
}

func (s Subset) Classify(keyword string, fineTuned bool) (kind, severity, action string) {
	if _, ok := s.supportedSet[keyword]; ok {
		if fineTuned {
			if _, extra := s.ftExtraSet[keyword]; extra {
				return KindUnsupportedKeyword, SeverityWarning, ActionReject
			}
		}
		return "", "", ""
	}
	if _, ok := s.unverifiedSet[keyword]; ok {
		return KindUnknownKeyword, SeverityInfo, ActionReport
	}
	if _, ok := s.unsupportedSet[keyword]; ok {
		return KindUnsupportedKeyword, SeverityWarning, ActionReject
	}
	if fineTuned {
		if _, extra := s.ftExtraSet[keyword]; extra {
			return KindUnsupportedKeyword, SeverityWarning, ActionReject
		}
	}
	return KindUnknownKeyword, SeverityWarning, ActionReject
}

func (s Subset) Hint(keyword string) string {
	if s.Hints == nil {
		return ""
	}
	if h := s.Hints[keyword]; h != "" {
		return h
	}
	if strings.HasPrefix(keyword, "x-") {
		return "OpenAI does not read vendor extension keywords; the model never sees them. Move useful text into `description`."
	}
	return "OpenAI's documented JSON Schema subset does not include `" + keyword + "`; the model is not guaranteed to see it."
}

func (s Subset) FormatAllowed(v string) bool {
	_, ok := s.formatSet[v]
	return ok
}

func setOf(items []string) map[string]struct{} {
	out := make(map[string]struct{}, len(items))
	for _, item := range items {
		out[item] = struct{}{}
	}
	return out
}

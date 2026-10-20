// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"net/url"
	"strings"
	"testing"
)

func TestOverlapJoinTable(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		base     string
		request  string
		wantPath string
		wantRawQ string
	}{
		// Repro rows from DOG-5 (echo upstream at /api/v1).
		{
			name:     "repro_v1_on_api_v1",
			base:     "http://fake/api/v1",
			request:  "/v1/chat/completions",
			wantPath: "/api/v1/chat/completions",
		},
		{
			name:     "repro_bare_chat",
			base:     "http://fake/api/v1",
			request:  "/chat/completions",
			wantPath: "/api/v1/chat/completions",
		},
		{
			name:     "repro_u_openrouter_after_route",
			base:     "http://fake/api/v1",
			request:  "/u/openrouter/chat/completions",
			wantPath: "/api/v1/chat/completions",
		},
		{
			name:     "openai_v1_plus_v1_chat",
			base:     "https://api.openai.com/v1",
			request:  "/v1/chat/completions",
			wantPath: "/v1/chat/completions",
		},
		{
			name:     "openai_origin_plus_v1_chat",
			base:     "https://api.openai.com",
			request:  "/v1/chat/completions",
			wantPath: "/v1/chat/completions",
		},
		{
			name:     "openai_v1_plus_u_openai_chat",
			base:     "https://api.openai.com/v1",
			request:  "/u/openai/chat/completions",
			wantPath: "/v1/chat/completions",
		},
		{
			name:     "openai_v1_plus_s_session_v1",
			base:     "https://api.openai.com/v1",
			request:  "/s/t1/v1/chat/completions",
			wantPath: "/v1/chat/completions",
		},
		{
			name:     "openrouter_plus_s_and_u",
			base:     "https://openrouter.ai/api/v1",
			request:  "/s/t1/u/openrouter/chat/completions",
			wantPath: "/api/v1/chat/completions",
		},
		{
			name:     "api_v1_plus_v1_chat",
			base:     "https://openrouter.ai/api/v1",
			request:  "/v1/chat/completions",
			wantPath: "/api/v1/chat/completions",
		},
		{
			name:     "openai_v1_plus_v1_responses",
			base:     "https://api.openai.com/v1",
			request:  "/v1/responses",
			wantPath: "/v1/responses",
		},
		{
			name:     "azure_openai_v1_plus_v1_responses",
			base:     "https://my-resource.openai.azure.com/openai/v1",
			request:  "/v1/responses",
			wantPath: "/openai/v1/responses",
		},
		{
			name:     "azure_openai_v1_plus_chat",
			base:     "https://my-resource.openai.azure.com/openai/v1",
			request:  "/u/azure/chat/completions",
			wantPath: "/openai/v1/chat/completions",
		},
		{
			name:     "azure_classic_deployment",
			base:     "https://my-resource.openai.azure.com",
			request:  "/openai/deployments/my-dep/chat/completions?api-version=2024-10-21",
			wantPath: "/openai/deployments/my-dep/chat/completions",
			wantRawQ: "api-version=2024-10-21",
		},
		{
			name:     "azure_classic_via_u",
			base:     "https://my-resource.openai.azure.com",
			request:  "/u/azure/openai/deployments/dep-2/chat/completions?api-version=2024-10-21",
			wantPath: "/openai/deployments/dep-2/chat/completions",
			wantRawQ: "api-version=2024-10-21",
		},
		{
			name:     "full_overlap_openai_v1",
			base:     "https://host/openai/v1",
			request:  "/openai/v1/chat/completions",
			wantPath: "/openai/v1/chat/completions",
		},
		{
			name:     "no_base_path",
			base:     "https://host",
			request:  "/v1/chat/completions",
			wantPath: "/v1/chat/completions",
		},
		{
			name:     "s_prefix_then_responses",
			base:     "https://api.openai.com",
			request:  "/s/abc/v1/responses",
			wantPath: "/v1/responses",
		},
		{
			name:     "trailing_slash_preserved",
			base:     "https://api.openai.com/v1",
			request:  "/v1/models/",
			wantPath: "/v1/models/",
		},
	}
	if len(tests) < 15 {
		t.Fatalf("join table must cover ≥15 pairs, got %d", len(tests))
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			base, err := url.Parse(tc.base)
			if err != nil {
				t.Fatal(err)
			}
			req, err := url.Parse(tc.request)
			if err != nil {
				t.Fatal(err)
			}
			rest := stripRoutingPrefixes(req.Path)
			got := JoinUpstreamURL(base, rest)
			got.RawQuery = req.RawQuery
			if got.Path != tc.wantPath {
				t.Fatalf("path=%q want %q (base=%s request=%s rest=%s)", got.Path, tc.wantPath, tc.base, tc.request, rest)
			}
			if got.RawQuery != tc.wantRawQ {
				t.Fatalf("query=%q want %q", got.RawQuery, tc.wantRawQ)
			}
		})
	}
}

func stripRoutingPrefixes(p string) string {
	clean := "/" + strings.TrimLeft(p, "/")
	for {
		next, ok := stripOneRoutingPrefix(clean)
		if !ok {
			return clean
		}
		clean = next
	}
}

func stripOneRoutingPrefix(p string) (string, bool) {
	p = "/" + strings.TrimLeft(p, "/")
	for _, prefix := range []string{"/u/", "/s/"} {
		if !strings.HasPrefix(p, prefix) {
			continue
		}
		rest := strings.TrimLeft(p[len(prefix):], "/")
		_, after, ok := strings.Cut(rest, "/")
		if !ok {
			return "/", true
		}
		return "/" + after, true
	}
	return p, false
}

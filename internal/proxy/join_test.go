// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"net/url"
	"sync/atomic"
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
		// Repro rows (echo upstream at /api/v1).
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
		{
			name:     "gemini_v1beta_openai_plus_client_v1",
			base:     "https://generativelanguage.googleapis.com/v1beta/openai",
			request:  "/v1/chat/completions",
			wantPath: "/v1beta/openai/chat/completions",
		},
		{
			name:     "gemini_via_u",
			base:     "https://generativelanguage.googleapis.com/v1beta/openai",
			request:  "/u/gemini/v1/chat/completions",
			wantPath: "/v1beta/openai/chat/completions",
		},
		{
			name:     "gemini_via_s",
			base:     "https://generativelanguage.googleapis.com/v1beta/openai",
			request:  "/s/sid/u/gemini/v1/chat/completions",
			wantPath: "/v1beta/openai/chat/completions",
		},
		{
			name:     "gemini_no_client_version",
			base:     "https://generativelanguage.googleapis.com/v1beta/openai",
			request:  "/u/gemini/chat/completions",
			wantPath: "/v1beta/openai/chat/completions",
		},
		{
			name:     "gemini_trailing_slash",
			base:     "https://generativelanguage.googleapis.com/v1beta/openai/",
			request:  "/v1/chat/completions/",
			wantPath: "/v1beta/openai/chat/completions/",
		},
		{
			name:     "gemini_query_preserved",
			base:     "https://generativelanguage.googleapis.com/v1beta/openai",
			request:  "/v1/chat/completions?alt=sse",
			wantPath: "/v1beta/openai/chat/completions",
			wantRawQ: "alt=sse",
		},
		{
			name:     "deepseek_origin_keeps_v1",
			base:     "https://api.deepseek.com",
			request:  "/v1/chat/completions",
			wantPath: "/v1/chat/completions",
		},
		{
			name:     "groq_openai_v1_plus_v1",
			base:     "https://api.groq.com/openai/v1",
			request:  "/v1/chat/completions",
			wantPath: "/openai/v1/chat/completions",
		},
		{
			name:     "fireworks_inference_v1_plus_v1",
			base:     "https://api.fireworks.ai/inference/v1",
			request:  "/v1/chat/completions",
			wantPath: "/inference/v1/chat/completions",
		},
		{
			name:     "cloudflare_ai_v1_plus_v1",
			base:     "https://api.cloudflare.com/client/v4/accounts/x/ai/v1",
			request:  "/v1/chat/completions",
			wantPath: "/client/v4/accounts/x/ai/v1/chat/completions",
		},
		{
			name:     "v1beta_base_plus_v1",
			base:     "https://host/v1beta",
			request:  "/v1/chat/completions",
			wantPath: "/v1beta/chat/completions",
		},
		{
			name:     "v1alpha_base_plus_v1",
			base:     "https://host/v1alpha",
			request:  "/v1/models",
			wantPath: "/v1alpha/models",
		},
		{
			name:     "uppercase_client_v1",
			base:     "https://generativelanguage.googleapis.com/v1beta/openai",
			request:  "/V1/chat/completions",
			wantPath: "/v1beta/openai/chat/completions",
		},
		{
			name:     "equal_v1beta_overlap_no_extra_drop",
			base:     "https://host/v1beta",
			request:  "/v1beta/chat/completions",
			wantPath: "/v1beta/chat/completions",
		},
		{
			name:     "base_no_version_keeps_client_v1",
			base:     "https://api.perplexity.ai",
			request:  "/v1/chat/completions",
			wantPath: "/v1/chat/completions",
		},
		{
			name:     "base_trailing_slash_gemini",
			base:     "https://generativelanguage.googleapis.com/v1beta/openai/",
			request:  "/v1/chat/completions",
			wantPath: "/v1beta/openai/chat/completions",
		},
		{
			name:     "overlap_must_not_strip_inner_version",
			base:     "http://fake/v1/foo",
			request:  "/foo/v1/chat",
			wantPath: "/v1/foo/v1/chat",
		},
		{
			name:     "leading_client_version_still_dropped",
			base:     "http://fake/v1/foo",
			request:  "/v1/chat",
			wantPath: "/v1/foo/chat",
		},
	}
	if len(tests) < 20 {
		t.Fatalf("join table must cover ≥20 pairs, got %d", len(tests))
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

func TestOverlapJoinVersionRewriteWarnsOnce(t *testing.T) {
	prev := versionJoinWarn
	var n atomic.Int32
	versionJoinWarn = func(basePath, restPath, joined string) {
		n.Add(1)
		if basePath != "/v1beta/openai" || restPath != "/v1/chat/completions" {
			t.Errorf("warn args base=%q rest=%q joined=%q", basePath, restPath, joined)
		}
		if joined != "/v1beta/openai/chat/completions" {
			t.Errorf("joined=%q", joined)
		}
	}
	t.Cleanup(func() {
		versionJoinWarn = prev
		versionJoinWarned.Store(false)
	})
	versionJoinWarned.Store(false)

	if got := overlapJoin("/v1beta/openai", "/v1/chat/completions"); got != "/v1beta/openai/chat/completions" {
		t.Fatalf("first join=%q", got)
	}
	if got := overlapJoin("/v1beta/openai", "/v1/models"); got != "/v1beta/openai/models" {
		t.Fatalf("second join=%q", got)
	}
	if got := overlapJoin("/openai/v1", "/v1/chat/completions"); got != "/openai/v1/chat/completions" {
		t.Fatalf("equal-overlap join=%q", got)
	}
	if got := n.Load(); got != 1 {
		t.Fatalf("warn count=%d want 1", got)
	}
}

# SPDX-License-Identifier: AGPL-3.0-only

.PHONY: gen spec-update spec-verify codegen-check

# Filter the vendored OpenAI spec and regenerate internal/openaiapi/gen.go.
# A second run must be byte-for-byte identical (no timestamps, stable key order).
gen:
	go run ./tools/gencode

# Download openai/openai-openapi@SPEC_SHA, refresh SPEC_VERSION, regenerate.
# Usage: make spec-update SPEC_SHA=<full commit sha>
spec-update:
	@if [ -z "$(SPEC_SHA)" ]; then echo "SPEC_SHA is required"; exit 2; fi
	go run ./tools/specupdate -sha "$(SPEC_SHA)"
	$(MAKE) gen

# Verify api/openai/openapi.json sha256 matches SPEC_VERSION.
spec-verify:
	go run ./tools/specverify

# CI entrypoint: spec pin, regenerate, fail on drift (including go.mod/go.sum).
codegen-check: spec-verify
	$(MAKE) gen
	git diff --exit-code
	go mod tidy
	git diff --exit-code
	$(MAKE) gen
	git diff --exit-code

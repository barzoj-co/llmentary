// SPDX-License-Identifier: AGPL-3.0-only

// specverify checks that api/openai/openapi.json matches SPEC_VERSION.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func main() {
	if err := verify(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func verify() error {
	meta, err := os.ReadFile("api/openai/SPEC_VERSION")
	if err != nil {
		return err
	}
	var (
		wantSHA     string
		wantVersion string
		wantCommit  string
	)
	for _, line := range strings.Split(string(meta), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "sha256:"):
			wantSHA = strings.TrimSpace(strings.TrimPrefix(line, "sha256:"))
		case strings.HasPrefix(line, "info.version:"):
			wantVersion = strings.TrimSpace(strings.TrimPrefix(line, "info.version:"))
		case strings.HasPrefix(line, "commit:"):
			wantCommit = strings.TrimSpace(strings.TrimPrefix(line, "commit:"))
		}
	}
	if wantSHA == "" || wantVersion == "" || wantCommit == "" {
		return fmt.Errorf("SPEC_VERSION must contain commit, info.version, and sha256")
	}
	raw, err := os.ReadFile("api/openai/openapi.json")
	if err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	got := hex.EncodeToString(sum[:])
	if got != wantSHA {
		return fmt.Errorf("openapi.json sha256 %s does not match SPEC_VERSION %s", got, wantSHA)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return err
	}
	info, _ := doc["info"].(map[string]any)
	version, _ := info["version"].(string)
	if version != wantVersion {
		return fmt.Errorf("openapi.json info.version %q does not match SPEC_VERSION %q", version, wantVersion)
	}
	fmt.Printf("ok commit=%s info.version=%s sha256=%s\n", wantCommit, version, got)
	return nil
}

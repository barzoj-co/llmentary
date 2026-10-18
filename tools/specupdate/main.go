// SPDX-License-Identifier: AGPL-3.0-only

// specupdate downloads a pinned openai/openai-openapi commit and rewrites
// api/openai/SPEC_VERSION. It does not run codegen; `make spec-update` does.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

func main() {
	sha := flag.String("sha", "", "openai/openai-openapi commit SHA")
	flag.Parse()
	if *sha == "" {
		fmt.Fprintln(os.Stderr, "usage: specupdate -sha <commit>")
		os.Exit(2)
	}
	if err := update(*sha); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func update(sha string) error {
	url := "https://raw.githubusercontent.com/openai/openai-openapi/" + sha + "/openapi.json"
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s", url, resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return fmt.Errorf("parse downloaded spec: %w", err)
	}
	info, _ := doc["info"].(map[string]any)
	version, _ := info["version"].(string)
	if version == "" {
		return fmt.Errorf("downloaded spec missing info.version")
	}
	if err := os.WriteFile("api/openai/openapi.json", body, 0o644); err != nil {
		return err
	}
	sum := sha256.Sum256(body)
	content := strings.Join([]string{
		"# Vendored openai/openai-openapi pin. CI checks sha256 of openapi.json against this file.",
		"commit: " + sha,
		"info.version: " + version,
		"sha256: " + hex.EncodeToString(sum[:]),
		"",
	}, "\n")
	return os.WriteFile("api/openai/SPEC_VERSION", []byte(content), 0o644)
}

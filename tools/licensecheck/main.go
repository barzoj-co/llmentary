// SPDX-License-Identifier: AGPL-3.0-only
// licensecheck fails unless LICENSE is detected as AGPL-3.0 at >= 98% coverage.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/licensecheck"
)

const minCoverage = 98.0

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(root, "LICENSE"))
	if err != nil {
		return fmt.Errorf("read LICENSE: %w", err)
	}
	cov := licensecheck.Scan(data)
	fmt.Printf("LICENSE coverage: %.2f%%\n", cov.Percent)
	found := false
	for _, m := range cov.Match {
		fmt.Printf("  match %s [%d:%d]\n", m.ID, m.Start, m.End)
		if m.ID == "AGPL-3.0" || strings.HasPrefix(m.ID, "AGPL-3.0") {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("LICENSE is not detected as AGPL-3.0")
	}
	if cov.Percent < minCoverage {
		return fmt.Errorf("LICENSE coverage %.2f%% is below %.0f%%", cov.Percent, minCoverage)
	}
	fmt.Println("license-detect: AGPL-3.0 ok")
	return nil
}

func repoRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "LICENSE")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("repo root with LICENSE not found from %s", wd)
		}
		dir = parent
	}
}

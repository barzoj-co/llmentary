// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"runtime"
	"strings"
	"testing"
)

func resetBuildInfo() {
	buildMu.Lock()
	buildVersion = "devel"
	buildCommit = ""
	buildDate = ""
	buildMu.Unlock()
}

func TestSetBuildInfoAndVersionLine(t *testing.T) {
	resetBuildInfo()
	t.Cleanup(resetBuildInfo)

	SetBuildInfo("v1.2.3", "abc1234", "2026-10-21")
	if VersionString() != "1.2.3" {
		t.Fatalf("VersionString=%q", VersionString())
	}
	if CommitString() != "abc1234" {
		t.Fatalf("CommitString=%q", CommitString())
	}
	line := VersionLine()
	for _, want := range []string{"llmentary 1.2.3", "abc1234", "2026-10-21", runtime.Version(), runtime.GOOS + "/" + runtime.GOARCH} {
		if !strings.Contains(line, want) {
			t.Fatalf("VersionLine missing %q: %s", want, line)
		}
	}
	m := versionJSON()
	if m["version"] != "1.2.3" || m["commit"] != "abc1234" || m["date"] != "2026-10-21" {
		t.Fatalf("versionJSON=%v", m)
	}
}

func TestSetBuildInfoIgnoresBlank(t *testing.T) {
	resetBuildInfo()
	t.Cleanup(resetBuildInfo)
	SetBuildInfo("0.4.0", "deadbeef", "today")
	SetBuildInfo("  ", "", "")
	if VersionString() != "0.4.0" {
		t.Fatalf("blank version overwrote: %q", VersionString())
	}
	if CommitString() != "deadbeef" {
		t.Fatalf("blank commit overwrote: %q", CommitString())
	}
}

func TestVersionLineUnknownPlaceholders(t *testing.T) {
	resetBuildInfo()
	t.Cleanup(resetBuildInfo)
	SetBuildInfo("devel", "", "")
	line := VersionLine()
	if !strings.HasPrefix(line, "llmentary devel (") {
		t.Fatalf("line=%s", line)
	}
	if !strings.Contains(line, runtime.Version()) {
		t.Fatalf("line=%s", line)
	}
}

func TestVersionLineIsTheHumanFormat(t *testing.T) {
	resetBuildInfo()
	t.Cleanup(resetBuildInfo)
	SetBuildInfo("0.5.0", "abc", "2026-10-22T00:00:00Z")
	line := VersionLine()
	if !strings.HasPrefix(line, "llmentary 0.5.0 (") {
		t.Fatalf("line=%s", line)
	}
	if VersionString() != "0.5.0" {
		t.Fatalf("semver=%s", VersionString())
	}
}

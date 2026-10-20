// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
)

var (
	buildMu      sync.RWMutex
	buildVersion = "devel"
	buildCommit  = ""
	buildDate    = ""
)

// SetBuildInfo records linker-injected version metadata for --version,
// GET /version, GET /config, and the startup log.
func SetBuildInfo(version, commit, date string) {
	buildMu.Lock()
	defer buildMu.Unlock()
	if strings.TrimSpace(version) != "" {
		buildVersion = strings.TrimPrefix(strings.TrimSpace(version), "v")
	}
	if strings.TrimSpace(commit) != "" {
		buildCommit = strings.TrimSpace(commit)
	}
	if strings.TrimSpace(date) != "" {
		buildDate = strings.TrimSpace(date)
	}
}

func currentBuildInfo() (version, commit, date string) {
	buildMu.RLock()
	version, commit, date = buildVersion, buildCommit, buildDate
	buildMu.RUnlock()
	if info, ok := debug.ReadBuildInfo(); ok {
		if version == "" || version == "devel" {
			mv := info.Main.Version
			if mv != "" && mv != "(devel)" && !strings.Contains(mv, "+dirty") && !strings.Contains(mv, "-0.") {
				version = strings.TrimPrefix(mv, "v")
			} else {
				version = "devel"
			}
		}
		if commit == "" {
			for _, setting := range info.Settings {
				if setting.Key == "vcs.revision" && setting.Value != "" {
					commit = setting.Value
					break
				}
			}
		}
		if date == "" {
			for _, setting := range info.Settings {
				if setting.Key == "vcs.time" && setting.Value != "" {
					date = setting.Value
					break
				}
			}
		}
	}
	if version == "" {
		version = "devel"
	}
	return version, commit, date
}

// VersionString is the clean semver (no v prefix). devel for go build / go install.
func VersionString() string {
	version, _, _ := currentBuildInfo()
	return version
}

// CommitString is the VCS revision when known.
func CommitString() string {
	_, commit, _ := currentBuildInfo()
	return commit
}

// VersionLine formats `llmentary <semver> (<commit>, <date>, <go version>, <os>/<arch>)`.
func VersionLine() string {
	version, commit, date := currentBuildInfo()
	if commit == "" {
		commit = "unknown"
	}
	if date == "" {
		date = "unknown"
	}
	return fmt.Sprintf("llmentary %s (%s, %s, %s, %s/%s)",
		version, commit, date, runtime.Version(), runtime.GOOS, runtime.GOARCH)
}

func versionJSON() map[string]string {
	version, commit, date := currentBuildInfo()
	return map[string]string{
		"version": version,
		"commit":  commit,
		"date":    date,
	}
}

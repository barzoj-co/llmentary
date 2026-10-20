// SPDX-License-Identifier: AGPL-3.0-only

// Package proxy implements the llmentary recording reverse proxy, control API,
// and OpenAI-wire parsers.
package proxy

import (
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
)

const (
	DefaultSpecValidation         = "warn"
	DefaultSpecProfile            = "relaxed"
	DefaultSpecValidationMaxBytes = 8 << 20
)

type Config struct {
	ListenAddr             string
	ControlAddr            string
	ControlToken           string
	Upstreams              map[string]*url.URL
	Logger                 *slog.Logger
	SpecValidation         string
	SpecProfile            string
	SpecValidationMaxBytes int
}

func ConfigFromEnv() (Config, error) {
	cfg := Config{
		ListenAddr:             getenv("LLMENTARY_LISTEN", ":7777"),
		ControlAddr:            getenv("LLMENTARY_CONTROL_LISTEN", "127.0.0.1:7778"),
		ControlToken:           os.Getenv("LLMENTARY_CONTROL_TOKEN"),
		Upstreams:              map[string]*url.URL{},
		SpecValidation:         getenv("LLMENTARY_SPEC_VALIDATION", DefaultSpecValidation),
		SpecProfile:            getenv("LLMENTARY_SPEC_PROFILE", DefaultSpecProfile),
		SpecValidationMaxBytes: DefaultSpecValidationMaxBytes,
	}
	if raw := strings.TrimSpace(os.Getenv("LLMENTARY_SPEC_VALIDATION_MAX_BYTES")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return cfg, fmt.Errorf("--spec-validation-max-bytes=%q is invalid; allowed values: non-negative byte count (env LLMENTARY_SPEC_VALIDATION_MAX_BYTES)", raw)
		}
		cfg.SpecValidationMaxBytes = n
	}
	raw := strings.TrimSpace(os.Getenv("LLMENTARY_UPSTREAMS"))
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv("LLMENTARY_UPSTREAM"))
	}
	if raw == "" {
		return cfg, nil
	}
	for _, item := range strings.Split(raw, ",") {
		if err := cfg.AddUpstream(item); err != nil {
			return cfg, err
		}
	}
	return cfg, nil
}

func (c *Config) AddUpstream(spec string) error {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil
	}
	name, rawURL, ok := strings.Cut(spec, "=")
	if !ok {
		name = "default"
		rawURL = spec
	}
	name = strings.Trim(strings.TrimSpace(name), "/")
	if name == "" {
		return fmt.Errorf("upstream name cannot be empty")
	}
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return err
	}
	if u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("upstream %q must include scheme and host", name)
	}
	if c.Upstreams == nil {
		c.Upstreams = map[string]*url.URL{}
	}
	c.Upstreams[name] = u
	return nil
}

func (c Config) Validate() error {
	if len(c.Upstreams) == 0 {
		return fmt.Errorf("configure at least one upstream with --upstream name=https://host/base or LLMENTARY_UPSTREAMS")
	}
	if !isLoopbackListenAddr(c.ControlAddr) && c.ControlToken == "" {
		return fmt.Errorf("control API on non-loopback address %q requires --control-token or LLMENTARY_CONTROL_TOKEN", c.ControlAddr)
	}
	if err := validateSpecSettings(orDefault(c.SpecValidation, DefaultSpecValidation), orDefault(c.SpecProfile, DefaultSpecProfile), c.SpecValidationMaxBytes); err != nil {
		return err
	}
	return nil
}

func orDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func validateSpecSettings(mode, profile string, maxBytes int) error {
	switch mode {
	case "off", "warn", "enforce":
	default:
		return fmt.Errorf("--spec-validation=%q is invalid; allowed values: off, warn, enforce (env LLMENTARY_SPEC_VALIDATION)", mode)
	}
	switch profile {
	case "strict", "relaxed":
	default:
		return fmt.Errorf("--spec-profile=%q is invalid; allowed values: strict, relaxed (env LLMENTARY_SPEC_PROFILE)", profile)
	}
	if maxBytes < 0 {
		return fmt.Errorf("--spec-validation-max-bytes=%d is invalid; allowed values: non-negative byte count (env LLMENTARY_SPEC_VALIDATION_MAX_BYTES)", maxBytes)
	}
	return nil
}

func isLoopbackListenAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = strings.TrimSpace(addr)
	}
	if host == "localhost" || strings.HasPrefix(host, "localhost:") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func getenv(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

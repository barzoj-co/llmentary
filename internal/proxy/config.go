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
	DefaultUpstreamName           = "openai"
	DefaultUpstreamURL            = "https://api.openai.com/v1"
	DefaultUpstreamSpec           = DefaultUpstreamName + "=" + DefaultUpstreamURL
	// DefaultUpstreamLog is the startup line written when no --upstream / env mapping is set.
	DefaultUpstreamLog = "no upstream configured; defaulting to openai=https://api.openai.com/v1"
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

// ApplyDefaultUpstream sets openai=https://api.openai.com/v1 when no upstream is configured.
// It returns true when the default was applied.
func (c *Config) ApplyDefaultUpstream() bool {
	if c == nil || len(c.Upstreams) > 0 {
		return false
	}
	if err := c.AddUpstream(DefaultUpstreamSpec); err != nil {
		return false
	}
	logger := slog.Default()
	if c.Logger != nil {
		logger = c.Logger
	}
	logger.Info(DefaultUpstreamLog)
	return true
}

func (c *Config) Validate() error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	c.ApplyDefaultUpstream()
	if err := c.validateNoUpstreamLoop(); err != nil {
		return err
	}
	if !isLoopbackListenAddr(c.ControlAddr) && c.ControlToken == "" {
		return fmt.Errorf("control API on non-loopback address %q requires --control-token or LLMENTARY_CONTROL_TOKEN", c.ControlAddr)
	}
	if err := validateSpecSettings(orDefault(c.SpecValidation, DefaultSpecValidation), orDefault(c.SpecProfile, DefaultSpecProfile), c.SpecValidationMaxBytes); err != nil {
		return err
	}
	return nil
}

func (c Config) validateNoUpstreamLoop() error {
	for name, u := range c.Upstreams {
		if u == nil {
			continue
		}
		if !isSelfUpstream(c.ListenAddr, u) {
			continue
		}
		return fmt.Errorf("upstream %q (%s) points at the proxy listen address %s; refusing to start a proxy loop", name, u.Redacted(), c.ListenAddr)
	}
	return nil
}

func isSelfUpstream(listenAddr string, upstream *url.URL) bool {
	if upstream == nil {
		return false
	}
	listenAddr = strings.TrimSpace(listenAddr)
	if listenAddr == "" {
		return false
	}
	lHost, lPort, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return false
	}
	if lPort == "" || lPort == "0" {
		return false
	}
	uHost := upstream.Hostname()
	uPort := upstream.Port()
	if uPort == "" {
		switch strings.ToLower(upstream.Scheme) {
		case "https":
			uPort = "443"
		default:
			uPort = "80"
		}
	}
	if lPort != uPort {
		return false
	}
	if sameListenHost(lHost, uHost) {
		return true
	}
	// Only treat loop aliases as the same listener when the proxy itself
	// is bound to a loop/unspecified host. A LAN listen (10.0.0.5:7777)
	// plus http://localhost:7777 is not a self-loop.
	return isLoopAliasHost(lHost) && isLoopAliasHost(uHost)
}

func sameListenHost(listenHost, upstreamHost string) bool {
	a := canonicalHost(listenHost)
	b := canonicalHost(upstreamHost)
	if a == b {
		return true
	}
	return isLoopAliasHost(listenHost) && isLoopAliasHost(upstreamHost)
}

func canonicalHost(host string) string {
	return strings.ToLower(strings.Trim(strings.TrimSpace(host), "[]"))
}

func isLoopAliasHost(host string) bool {
	h := canonicalHost(host)
	switch h {
	case "", "localhost", "0.0.0.0", "::", "*":
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && (ip.IsLoopback() || ip.IsUnspecified())
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

package lab

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

type Config struct {
	ListenAddr  string
	ControlAddr string
	Upstreams   map[string]*url.URL
}

func ConfigFromEnv() (Config, error) {
	cfg := Config{
		ListenAddr:  getenv("LLMTESTLAB_LISTEN", ":7777"),
		ControlAddr: getenv("LLMTESTLAB_CONTROL_LISTEN", ":7778"),
		Upstreams:   map[string]*url.URL{},
	}
	raw := strings.TrimSpace(os.Getenv("LLMTESTLAB_UPSTREAMS"))
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv("LLMTESTLAB_UPSTREAM"))
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
		return fmt.Errorf("configure at least one upstream with --upstream name=https://host/base or LLMTESTLAB_UPSTREAMS")
	}
	return nil
}

func getenv(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

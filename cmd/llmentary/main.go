// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/barzoj-co/llmentary/internal/proxy"
)

// Set by GoReleaser -X ldflags on tagged releases. go build / go install
// fall back to debug.ReadBuildInfo (devel + vcs revision).
var (
	version = "devel"
	commit  = ""
	date    = ""
)

// listenAndServe is replaced in tests so run() can be exercised without binding.
var listenAndServe = func(ctx context.Context, server *proxy.Server) error {
	return server.ListenAndServe(ctx)
}

type upstreamFlags []string

func (u *upstreamFlags) String() string {
	return strings.Join(*u, ",")
}

func (u *upstreamFlags) Set(value string) error {
	*u = append(*u, value)
	return nil
}

type enumFlag string

func (f *enumFlag) String() string { return string(*f) }

func (f *enumFlag) Set(value string) error {
	*f = enumFlag(value)
	return nil
}

type nonNegIntFlag int

func (f *nonNegIntFlag) String() string { return fmt.Sprintf("%d", int(*f)) }

func (f *nonNegIntFlag) Set(value string) error {
	n, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("--spec-validation-max-bytes=%q is invalid; allowed values: non-negative byte count", value)
	}
	if n < 0 {
		return fmt.Errorf("--spec-validation-max-bytes=%d is invalid; allowed values: non-negative byte count", n)
	}
	*f = nonNegIntFlag(n)
	return nil
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	proxy.SetBuildInfo(version, commit, date)
	for _, arg := range args {
		if arg == "-version" || arg == "--version" {
			_, _ = fmt.Fprintln(stdout, proxy.VersionLine())
			return 0
		}
	}
	cfg, err := proxy.ConfigFromEnv()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	fs := flag.NewFlagSet("llmentary", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var upstreams upstreamFlags
	showVersion := fs.Bool("version", false, "print version and exit")
	fs.StringVar(&cfg.ListenAddr, "listen", cfg.ListenAddr, "proxy listen address (env LLMENTARY_LISTEN)")
	fs.StringVar(&cfg.ControlAddr, "control", cfg.ControlAddr, "control API listen address (env LLMENTARY_CONTROL_LISTEN)")
	fs.StringVar(&cfg.ControlToken, "control-token", cfg.ControlToken, "bearer token required for control API; required for non-loopback control binds (env LLMENTARY_CONTROL_TOKEN)")
	fs.Var(&upstreams, "upstream", "upstream mapping name=https://host/base; repeatable (env LLMENTARY_UPSTREAMS)")
	fs.Var((*enumFlag)(&cfg.SpecValidation), "spec-validation", "request spec validation mode: off, warn, enforce (env LLMENTARY_SPEC_VALIDATION)")
	fs.Var((*enumFlag)(&cfg.SpecProfile), "spec-profile", "spec validation profile: strict, relaxed (env LLMENTARY_SPEC_PROFILE)")
	fs.Var((*nonNegIntFlag)(&cfg.SpecValidationMaxBytes), "spec-validation-max-bytes", "max request bytes to validate (env LLMENTARY_SPEC_VALIDATION_MAX_BYTES)")
	specLogMax := proxy.DefaultSpecLogMax
	if cfg.SpecLogMax != nil {
		specLogMax = *cfg.SpecLogMax
	}
	fs.IntVar(&specLogMax, "spec-log-max", specLogMax, "max per-finding WARN lines after the summary; 0 = summary only (env LLMENTARY_SPEC_LOG_MAX)")
	sessionFrom := strings.Join(cfg.SessionFrom, ",")
	if sessionFrom == "" {
		sessionFrom = proxy.DefaultSessionFrom
	}
	fs.StringVar(&sessionFrom, "session-from", sessionFrom, "ordered session sources: header,baggage,path,default (env LLMENTARY_SESSION_FROM)")
	fs.StringVar(&cfg.DefaultSession, "default-session", cfg.DefaultSession, "session id for untagged calls when default is in --session-from (env LLMENTARY_DEFAULT_SESSION)")
	var allowPrefixes upstreamFlags
	fs.Var(&allowPrefixes, "allow-path-prefix", "extra first path segment allowed after /s and /u are stripped; repeatable (env LLMENTARY_ALLOW_PATH_PREFIXES)")
	fs.StringVar(&cfg.PropagateTrace, "propagate-trace", cfg.PropagateTrace, "outbound traceparent: off (default, pass client headers through) or auto (inject/continue) (env LLMENTARY_PROPAGATE_TRACE)")
	fs.Bool("tls", false, "TODO: optional local TLS listener")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	cfg.SpecLogMax = &specLogMax
	if *showVersion {
		_, _ = fmt.Fprintln(stdout, proxy.VersionLine())
		return 0
	}
	sources, err := proxy.ParseSessionFrom(sessionFrom)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		fs.Usage()
		return 2
	}
	cfg.SessionFrom = sources
	for _, p := range allowPrefixes {
		cfg.AllowPathPrefixes = append(cfg.AllowPathPrefixes, proxy.ParseAllowPathPrefixes(p)...)
	}
	for _, spec := range upstreams {
		if err := cfg.AddUpstream(spec); err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
	}
	if err := cfg.Validate(); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		fs.Usage()
		return 2
	}
	server, err := proxy.NewServer(cfg, proxy.NewMemoryStore(), proxy.TolerantParser{})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	_, _ = fmt.Fprintf(stderr, "llmentary %s proxy listening on %s; control API on %s\n", proxy.VersionString(), cfg.ListenAddr, cfg.ControlAddr)
	if err := listenAndServe(ctx, server); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

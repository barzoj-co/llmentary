// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
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
	proxy.SetBuildInfo(version, commit, date)
	for _, arg := range os.Args[1:] {
		if arg == "-version" || arg == "--version" {
			fmt.Println(proxy.VersionLine())
			os.Exit(0)
		}
	}
	cfg, err := proxy.ConfigFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	var upstreams upstreamFlags
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.StringVar(&cfg.ListenAddr, "listen", cfg.ListenAddr, "proxy listen address (env LLMENTARY_LISTEN)")
	flag.StringVar(&cfg.ControlAddr, "control", cfg.ControlAddr, "control API listen address (env LLMENTARY_CONTROL_LISTEN)")
	flag.StringVar(&cfg.ControlToken, "control-token", cfg.ControlToken, "bearer token required for control API; required for non-loopback control binds (env LLMENTARY_CONTROL_TOKEN)")
	flag.Var(&upstreams, "upstream", "upstream mapping name=https://host/base; repeatable (env LLMENTARY_UPSTREAMS)")
	flag.Var((*enumFlag)(&cfg.SpecValidation), "spec-validation", "request spec validation mode: off, warn, enforce (env LLMENTARY_SPEC_VALIDATION)")
	flag.Var((*enumFlag)(&cfg.SpecProfile), "spec-profile", "spec validation profile: strict, relaxed (env LLMENTARY_SPEC_PROFILE)")
	flag.Var((*nonNegIntFlag)(&cfg.SpecValidationMaxBytes), "spec-validation-max-bytes", "max request bytes to validate (env LLMENTARY_SPEC_VALIDATION_MAX_BYTES)")
	specLogMax := proxy.DefaultSpecLogMax
	if cfg.SpecLogMax != nil {
		specLogMax = *cfg.SpecLogMax
	}
	flag.IntVar(&specLogMax, "spec-log-max", specLogMax, "max per-finding WARN lines after the summary; 0 = summary only (env LLMENTARY_SPEC_LOG_MAX)")
	flag.Bool("tls", false, "TODO: optional local TLS listener")
	flag.Parse()
	cfg.SpecLogMax = &specLogMax
	if *showVersion {
		fmt.Println(proxy.VersionLine())
		os.Exit(0)
	}
	for _, spec := range upstreams {
		if err := cfg.AddUpstream(spec); err != nil {
			log.Fatal(err)
		}
	}
	if err := cfg.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		flag.Usage()
		os.Exit(2)
	}
	server, err := proxy.NewServer(cfg, proxy.NewMemoryStore(), proxy.TolerantParser{})
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Printf("llmentary %s proxy listening on %s; control API on %s", proxy.VersionString(), cfg.ListenAddr, cfg.ControlAddr)
	if err := server.ListenAndServe(ctx); err != nil {
		log.Fatal(err)
	}
}

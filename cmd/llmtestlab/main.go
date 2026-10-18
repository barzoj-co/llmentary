// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/barzoj-co/llm-test-lab/internal/lab"
)

type upstreamFlags []string

func (u *upstreamFlags) String() string {
	return strings.Join(*u, ",")
}

func (u *upstreamFlags) Set(value string) error {
	*u = append(*u, value)
	return nil
}

func main() {
	cfg, err := lab.ConfigFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	var upstreams upstreamFlags
	flag.StringVar(&cfg.ListenAddr, "listen", cfg.ListenAddr, "proxy listen address (env LLMTESTLAB_LISTEN)")
	flag.StringVar(&cfg.ControlAddr, "control", cfg.ControlAddr, "control API listen address (env LLMTESTLAB_CONTROL_LISTEN)")
	flag.StringVar(&cfg.ControlToken, "control-token", cfg.ControlToken, "bearer token required for control API; required for non-loopback control binds (env LLMTESTLAB_CONTROL_TOKEN)")
	flag.Var(&upstreams, "upstream", "upstream mapping name=https://host/base; repeatable (env LLMTESTLAB_UPSTREAMS)")
	flag.Bool("tls", false, "TODO: optional local TLS listener")
	flag.Parse()
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
	server, err := lab.NewServer(cfg, lab.NewMemoryStore(), lab.TolerantParser{})
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Printf("llm-test-lab proxy listening on %s; control API on %s", cfg.ListenAddr, cfg.ControlAddr)
	if err := server.ListenAndServe(ctx); err != nil {
		log.Fatal(err)
	}
}

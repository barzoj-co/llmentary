// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	// MaxUpstreamTimeout is the longest allowed header or idle timeout.
	MaxUpstreamTimeout = 10 * time.Minute
)

// Sentinel errors for optional upstream timeouts. Callers should use errors.Is.
var (
	ErrUpstreamHeaderTimeout = errors.New("upstream did not send response headers in time")
	ErrUpstreamIdleTimeout   = errors.New("upstream stopped sending data")
)

func headerTimeoutError(d time.Duration) error {
	return fmt.Errorf("%w (%s)", ErrUpstreamHeaderTimeout, d)
}

func idleTimeoutError(d time.Duration) error {
	return fmt.Errorf("%w (idle %s)", ErrUpstreamIdleTimeout, d)
}

func isContextTimeout(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// ParseOptionalTimeout accepts a Go duration, a bare second count, or 0/empty
// (disabled). Negative values and anything above MaxUpstreamTimeout fail.
func ParseOptionalTimeout(flag, env, raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	var d time.Duration
	if parsed, err := time.ParseDuration(raw); err == nil {
		d = parsed
	} else if sec, ferr := strconv.ParseFloat(raw, 64); ferr == nil {
		d = time.Duration(sec * float64(time.Second))
	} else {
		return 0, fmt.Errorf("--%s=%q is invalid; allowed values: duration (0 disables, max 10m) (env %s)", flag, raw, env)
	}
	if d < 0 {
		return 0, fmt.Errorf("--%s=%q is invalid; allowed values: duration (0 disables, max 10m) (env %s)", flag, raw, env)
	}
	if d > MaxUpstreamTimeout {
		return 0, fmt.Errorf("--%s=%q exceeds 10m (env %s)", flag, raw, env)
	}
	return d, nil
}

func validateUpstreamTimeout(flag, env string, d time.Duration) error {
	if d < 0 {
		return fmt.Errorf("--%s=%s is invalid; allowed values: duration (0 disables, max 10m) (env %s)", flag, d, env)
	}
	if d > MaxUpstreamTimeout {
		return fmt.Errorf("--%s=%s exceeds 10m (env %s)", flag, d, env)
	}
	return nil
}

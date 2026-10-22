// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"strings"
	"testing"
)

func TestWaitTimeoutHint(t *testing.T) {
	t.Parallel()
	if got := waitTimeoutHint(nil); got != "Raise LLMENTARY_WAIT_TIMEOUT if the app needs more time." {
		t.Fatalf("empty: %q", got)
	}
	header := waitTimeoutHint([]InFlightCall{{
		Phase: InFlightPhaseAwaitingHeaders, ElapsedMS: 1395,
	}})
	if header != "the upstream has not sent headers for 1.4s; set LLMENTARY_UPSTREAM_HEADER_TIMEOUT to fail such calls fast" {
		t.Fatalf("header: %q", header)
	}
	if strings.Contains(header, "LLMENTARY_WAIT_TIMEOUT") {
		t.Fatal("header hint must not mention WAIT_TIMEOUT")
	}
	stream := waitTimeoutHint([]InFlightCall{{
		Phase: InFlightPhaseStreaming, ElapsedMS: 2000,
	}})
	if stream != "an upstream stream has been in progress for 2.0s; set LLMENTARY_UPSTREAM_IDLE_TIMEOUT to fail stuck streams" {
		t.Fatalf("stream: %q", stream)
	}
	if strings.Contains(stream, "LLMENTARY_WAIT_TIMEOUT") {
		t.Fatal("stream hint must not mention WAIT_TIMEOUT")
	}
	mixed := waitTimeoutHint([]InFlightCall{
		{Phase: InFlightPhaseStreaming, ElapsedMS: 5000},
		{Phase: InFlightPhaseAwaitingHeaders, ElapsedMS: 1395},
	})
	if mixed != header {
		t.Fatalf("mixed should prefer headers: %q", mixed)
	}
	progress := waitTimeoutHint([]InFlightCall{{Phase: "unknown", ElapsedMS: 10}})
	if progress != "Raise LLMENTARY_WAIT_TIMEOUT if the app needs more time." {
		t.Fatalf("progress: %q", progress)
	}
}

func TestFormatElapsedMS(t *testing.T) {
	t.Parallel()
	if formatElapsedMS(20) != "20ms" {
		t.Fatal(formatElapsedMS(20))
	}
	if formatElapsedMS(1000) != "1.0s" {
		t.Fatal(formatElapsedMS(1000))
	}
}

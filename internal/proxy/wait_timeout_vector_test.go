// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWaitTimeoutInFlightVectorKeys(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "..", "api", "control", "testdata", "wait_timeout_inflight.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"awaiting_headers", "streaming"} {
		example, _ := doc[name].(map[string]any)
		if example == nil {
			t.Fatalf("missing %s", name)
		}
		assertWaitTimeoutVectorShape(t, name, example, name == "streaming")
	}
}

func assertWaitTimeoutVectorShape(t *testing.T, name string, example map[string]any, streaming bool) {
	t.Helper()
	if _, ok := example["id"]; !ok {
		t.Fatalf("%s missing id", name)
	}
	if _, ok := example["calls"]; !ok {
		t.Fatalf("%s missing calls", name)
	}
	errObj, _ := example["error"].(map[string]any)
	for _, key := range []string{"type", "message", "calls_seen", "min_calls", "active", "in_flight"} {
		if _, ok := errObj[key]; !ok {
			t.Fatalf("%s error missing %s", name, key)
		}
	}
	if errObj["type"] != "wait_timeout" {
		t.Fatalf("%s type=%v", name, errObj["type"])
	}
	items, _ := errObj["in_flight"].([]any)
	if len(items) != 1 {
		t.Fatalf("%s in_flight=%v", name, errObj["in_flight"])
	}
	item, _ := items[0].(map[string]any)
	for _, key := range []string{"call_id", "seq", "method", "path", "api_family", "upstream", "started_at", "elapsed_ms", "phase", "bytes_received"} {
		if _, ok := item[key]; !ok {
			t.Fatalf("%s in_flight missing %s", name, key)
		}
	}
	if streaming {
		if item["phase"] != InFlightPhaseStreaming {
			t.Fatalf("%s phase=%v", name, item["phase"])
		}
		if item["http_status"] != float64(200) {
			t.Fatalf("%s http_status=%v", name, item["http_status"])
		}
	} else if item["phase"] != InFlightPhaseAwaitingHeaders {
		t.Fatalf("%s phase=%v", name, item["phase"])
	}
}

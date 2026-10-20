// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"testing"
	"time"
)

func TestParseControlDuration(t *testing.T) {
	d90, err := parseControlDuration("timeout", "90")
	if err != nil || d90 != 90*time.Second {
		t.Fatalf("90: %v %s", err, d90)
	}
	d90s, err := parseControlDuration("timeout", "90s")
	if err != nil || d90s != 90*time.Second {
		t.Fatalf("90s: %v %s", err, d90s)
	}
	if d90 != d90s {
		t.Fatal("90 and 90s must be equivalent")
	}
	dFrac, err := parseControlDuration("quiet", "0.2")
	if err != nil || dFrac != 200*time.Millisecond {
		t.Fatalf("0.2: %v %s", err, dFrac)
	}
	if _, err := parseControlDuration("timeout", "abc"); err == nil {
		t.Fatal("abc should fail")
	}
	if _, err := parseControlDuration("timeout", "0"); err == nil {
		t.Fatal("0 should fail")
	}
	if _, err := parseControlDuration("timeout", "11m"); err == nil {
		t.Fatal("11m should fail")
	}
}

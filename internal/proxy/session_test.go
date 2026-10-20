// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"net/http"
	"testing"
)

func TestSessionFromBaggageAndStripLlmentaryMembers(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "http://example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("baggage", "llmentary.session.id=baggage-session, other=value, llmentary.debug=true")
	session := sessionFromRequest(req)
	if session.ID != "baggage-session" || session.Source != "baggage" {
		t.Fatalf("session = %+v", session)
	}
	stripLlmentaryBaggage(req.Header)
	if got := req.Header.Get("baggage"); got != "other=value" {
		t.Fatalf("baggage = %q, want other=value", got)
	}
}

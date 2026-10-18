// SPDX-License-Identifier: AGPL-3.0-only

package lab

import (
	"net/http"
	"testing"
)

func TestSessionFromBaggageAndStripLLMTestLabMembers(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "http://example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("baggage", "llmtestlab.session.id=baggage-session, other=value, llmtestlab.debug=true")
	session := sessionFromRequest(req)
	if session.ID != "baggage-session" || session.Source != "baggage" {
		t.Fatalf("session = %+v", session)
	}
	stripLLMTestLabBaggage(req.Header)
	if got := req.Header.Get("baggage"); got != "other=value" {
		t.Fatalf("baggage = %q, want other=value", got)
	}
}

// SPDX-License-Identifier: AGPL-3.0-only

package specvalidate

import "testing"

func TestCompileEmbeddedSmoke(t *testing.T) {
	eng, err := CompileEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("spec_sha=%s already_closed=%d closed_by_transform=%d skipped=%d", eng.SpecSHA, len(eng.Report.AlreadyClosed), len(eng.Report.ClosedByTransform), len(eng.Report.SkippedFreeForm))
	cleanChat := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	if res := eng.ValidateRequest(ProfileStrict, "openai.chat", cleanChat, 0); requestViolations(res) != 0 {
		t.Fatalf("clean chat strict violations=%v", res.Violations)
	}
	cleanResp := []byte(`{"model":"m","input":"hi"}`)
	if res := eng.ValidateRequest(ProfileStrict, "openai.responses", cleanResp, 0); requestViolations(res) != 0 {
		t.Fatalf("clean responses strict violations=%v", res.Violations)
	}
	res := eng.ValidateRequest(ProfileStrict, "openai.chat", []byte(`{"foo":1}`), 0)
	if len(res.Violations) == 0 {
		t.Fatal("expected strict violations for unknown top-level foo")
	}
	for _, v := range res.Violations {
		t.Logf("strict foo: keyword=%s pointer=%s path=%s msg=%s", v.Keyword, v.Pointer, v.Path, v.Message)
	}
	res = eng.ValidateRequest(ProfileRelaxed, "openai.chat", []byte(`{"model":"m","messages":[{"role":"user","content":"hi","foo":1}]}`), 0)
	if requestViolations(res) != 0 {
		t.Fatalf("relaxed must reopen chat messages: %v", res.Violations)
	}
	res = eng.ValidateRequest(ProfileStrict, "openai.chat", []byte(`{"model":"m","messages":[{"role":"user","content":"hi","foo":1}]}`), 0)
	found := false
	for _, v := range res.Violations {
		if v.Keyword == "unevaluatedProperties" && (v.Pointer == "/messages/0/foo" || v.Path == "messages[0].foo") {
			found = true
		}
		t.Logf("strict msg.foo: keyword=%s pointer=%s msg=%s", v.Keyword, v.Pointer, v.Message)
	}
	if !found {
		t.Fatalf("strict must flag messages[0].foo: %v", res.Violations)
	}
}

func requestViolations(res Result) int {
	n := 0
	for _, v := range res.Violations {
		if v.Side != SideResponse {
			n++
		}
	}
	return n
}

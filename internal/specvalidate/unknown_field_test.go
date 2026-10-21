// SPDX-License-Identifier: AGPL-3.0-only

package specvalidate

import (
	"strings"
	"testing"
)

func TestUnknownFieldMessageNamesField(t *testing.T) {
	eng, err := CompileEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	res := eng.ValidateRequest(ProfileStrict, "openai.chat", []byte(`{"temprature":0}`), 0)
	found := false
	for _, v := range res.Violations {
		if v.Keyword != "unevaluatedProperties" || v.Pointer != "/temprature" {
			continue
		}
		found = true
		if !strings.Contains(v.Message, `unknown field "temprature"`) {
			t.Fatalf("message=%q", v.Message)
		}
		if strings.Contains(v.Message, "false schema") {
			t.Fatalf("still using library wording: %q", v.Message)
		}
		if strings.Contains(v.Message, "0") {
			t.Fatalf("message leaked field value: %q", v.Message)
		}
	}
	if !found {
		t.Fatalf("missing unevaluatedProperties /temprature: %v", res.Violations)
	}
}

func TestUnknownFieldMessageNumericKey(t *testing.T) {
	eng, err := CompileEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	res := eng.ValidateRequest(ProfileStrict, "openai.chat", []byte(`{"123":true}`), 0)
	found := false
	for _, v := range res.Violations {
		if v.Keyword != "unevaluatedProperties" || v.Pointer != "/123" {
			continue
		}
		found = true
		if !strings.Contains(v.Message, `unknown field "123"`) {
			t.Fatalf("message=%q", v.Message)
		}
		if strings.Contains(v.Message, "false schema") {
			t.Fatalf("still using library wording: %q", v.Message)
		}
	}
	if !found {
		t.Fatalf("missing unevaluatedProperties /123: %v", res.Violations)
	}
}

func TestLastPointerFieldKeepsNumericObjectKey(t *testing.T) {
	if got := lastPointerField("/123"); got != "123" {
		t.Fatalf("got %q", got)
	}
	if got := lastPointerField("/messages/0/123"); got != "123" {
		t.Fatalf("nested got %q", got)
	}
	if got := lastPointerField("/foo~1bar"); got != "foo/bar" {
		t.Fatalf("escaped got %q", got)
	}
}

func TestCollapseDuplicatesCounts(t *testing.T) {
	idx0, idx1, idx2 := 0, 1, 2
	in := []Violation{
		{Side: SideResponse, Keyword: "required", Pointer: "/", Message: "missing property 'created'", EventIndex: &idx0},
		{Side: SideResponse, Keyword: "required", Pointer: "/", Message: "missing property 'created'", EventIndex: &idx1},
		{Side: SideResponse, Keyword: "required", Pointer: "/", Message: "missing property 'created'", EventIndex: &idx2},
		{Side: SideRequest, Keyword: "unevaluatedProperties", Pointer: "/foo", Message: `unknown field "foo"`},
	}
	out := CollapseDuplicates(in)
	if len(out) != 2 {
		t.Fatalf("len=%d want 2: %+v", len(out), out)
	}
	if out[0].Count != 3 || out[0].EventIndex == nil || *out[0].EventIndex != 0 {
		t.Fatalf("collapsed=%+v", out[0])
	}
	if out[1].Count != 1 || out[1].Pointer != "/foo" {
		t.Fatalf("request=%+v", out[1])
	}
}

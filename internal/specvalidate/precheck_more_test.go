// SPDX-License-Identifier: AGPL-3.0-only

package specvalidate

import (
	"strings"
	"testing"
)

func TestPrechecksSyntaxCorners(t *testing.T) {
	t.Parallel()
	cfg := precheckConfig{}
	empty := prechecks([]byte("   "), ProfileStrict, cfg)
	if !hasKeyword(empty, "emptyBody") {
		t.Fatalf("empty=%v", empty)
	}
	bom := prechecks(append([]byte{0xEF, 0xBB, 0xBF}, '{', '}'), ProfileStrict, cfg)
	if !hasKeyword(bom, "utf8BOM") {
		t.Fatalf("bom=%v", bom)
	}
	utf := prechecks([]byte{0xff}, ProfileStrict, cfg)
	if !hasKeyword(utf, "invalidUTF8") {
		t.Fatalf("utf8=%v", utf)
	}
	trailing := prechecks([]byte(`{}  true`), ProfileStrict, cfg)
	if !hasKeyword(trailing, "trailingData") {
		t.Fatalf("trailing=%v", trailing)
	}
	notObj := prechecks([]byte(`[]`), ProfileStrict, cfg)
	if !hasKeyword(notObj, "type") {
		t.Fatalf("array=%v", notObj)
	}
	badJSON := prechecks([]byte(`{`), ProfileStrict, cfg)
	if !hasKeyword(badJSON, "trailingData") {
		t.Fatalf("bad json=%v", badJSON)
	}

	skip := prechecks([]byte("   "), ProfileRelaxed, precheckConfig{skip: map[string]bool{"emptyBody": true}})
	if hasKeyword(skip, "emptyBody") {
		t.Fatal("skip emptyBody")
	}

	deep := strings.Repeat("[", 130) + strings.Repeat("]", 130)
	if !hasKeyword(prechecks([]byte(deep), ProfileStrict, cfg), "maxDepth") {
		t.Fatal("expected maxDepth")
	}

	inf := prechecks([]byte(`{"n":1e400}`), ProfileStrict, cfg)
	if !hasKeyword(inf, "numberRange") {
		t.Fatalf("numberRange=%v", inf)
	}

	s := rawScanner{data: []byte("  "), cfg: cfg, out: &[]Violation{}}
	s.skipValue()
	if s.pos != len(s.data) {
		t.Fatalf("skip ws pos=%d", s.pos)
	}
	s = rawScanner{data: []byte(`"hi"`), cfg: cfg, out: &[]Violation{}}
	s.skipValue()
	if s.pos != 4 {
		t.Fatalf("skip string pos=%d", s.pos)
	}
	s = rawScanner{data: []byte(`true`), cfg: cfg, out: &[]Violation{}}
	s.skipValue()
	if s.pos != 4 {
		t.Fatalf("skip literal pos=%d", s.pos)
	}
	s = rawScanner{data: []byte(`-12`), cfg: cfg, out: &[]Violation{}}
	s.skipValue()
	if s.pos != 3 {
		t.Fatalf("skip number pos=%d", s.pos)
	}
	s = rawScanner{data: []byte(`{"a":[1,"x"]}`), cfg: cfg, out: &[]Violation{}}
	s.skipValue()
	if s.pos != len(s.data) {
		t.Fatalf("skip object pos=%d", s.pos)
	}
	s = rawScanner{data: []byte(`["a\"b",{"k":1}]`), cfg: cfg, out: &[]Violation{}}
	s.skipContainer()
	if s.pos != len(s.data) {
		t.Fatalf("skip container pos=%d", s.pos)
	}
	s = rawScanner{data: []byte(`x`), cfg: cfg, out: &[]Violation{}}
	s.skipContainer()
	if s.pos != 0 {
		t.Fatalf("skip non-container pos=%d", s.pos)
	}
	if invalidUTF8Offset([]byte("abc")) != 0 {
		t.Fatal("valid utf8")
	}
	if indexNonWS([]byte(" \n")) != -1 || indexNonWS([]byte(" a")) != 1 {
		t.Fatal("indexNonWS")
	}
}

func TestPointerToPathAndCap(t *testing.T) {
	t.Parallel()
	if PointerToPath("") != "" {
		t.Fatal("empty")
	}
	if PointerToPath("/messages/0/content") != "messages[0].content" {
		t.Fatalf("%s", PointerToPath("/messages/0/content"))
	}
	if isIndex("") || !isIndex("0") || !isIndex("00") || isIndex("a") {
		t.Fatal("isIndex")
	}
	trunc := false
	out := capAndSort(make([]Violation, MaxFindings+5), &trunc, SideRequest)
	if !trunc || len(out) != MaxFindings+1 || out[len(out)-1].Keyword != "truncated" {
		t.Fatalf("cap %d trunc=%v last=%+v", len(out), trunc, out[len(out)-1])
	}
	dups := CollapseDuplicates([]Violation{
		{Pointer: "/a", Keyword: "required", Message: "m", Side: SideRequest},
		{Pointer: "/a", Keyword: "required", Message: "m", Side: SideRequest},
		{Pointer: "/b", Keyword: "type", Message: "n", Side: SideRequest},
	})
	if len(dups) != 2 {
		t.Fatalf("dups=%v", dups)
	}
}

func hasKeyword(vs []Violation, keyword string) bool {
	for _, v := range vs {
		if v.Keyword == keyword {
			return true
		}
	}
	return false
}

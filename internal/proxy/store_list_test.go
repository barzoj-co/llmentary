// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"encoding/json"
	"testing"
	"time"
)

func TestSpecCountersUnmarshalJSONAliases(t *testing.T) {
	t.Parallel()
	var camel SpecCounters
	if err := json.Unmarshal([]byte(`{"checked":1,"clean":1,"findings":0,"request_only":2,"not_checked":3}`), &camel); err != nil {
		t.Fatal(err)
	}
	if camel.Checked != 1 || camel.Clean != 1 || camel.RequestOnly != 2 || camel.NotChecked != 3 {
		t.Fatalf("%+v", camel)
	}
	var pascal SpecCounters
	if err := json.Unmarshal([]byte(`{"Checked":4,"Clean":2,"Findings":2,"RequestOnly":1,"NotChecked":0}`), &pascal); err != nil {
		t.Fatal(err)
	}
	if pascal.Checked != 4 || pascal.Findings != 2 || pascal.RequestOnly != 1 {
		t.Fatalf("%+v", pascal)
	}
	if err := json.Unmarshal([]byte(`[]`), &camel); err == nil {
		t.Fatal("expected type error")
	}
	if firstInt(map[string]int{"B": 2}, "A", "B") != 2 || firstInt(nil, "A") != 0 {
		t.Fatal("firstInt")
	}
}

func TestMemoryStoreListFilterAndSort(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	t1 := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Hour)
	_, a := store.Begin("later", Call{StartedAt: t2, SessionSource: "header"})
	store.Finish("later", a, func(c *Call) { c.EndedAt = t2 })
	_, b := store.Begin("earlier", Call{StartedAt: t1, SessionSource: "path"})
	store.Finish("earlier", b, func(c *Call) { c.EndedAt = t1 })
	_, c := store.Begin("also-early", Call{StartedAt: t1, SessionSource: "header"})
	store.Finish("also-early", c, func(call *Call) { call.EndedAt = t1 })

	all := store.List("")
	if len(all) != 3 || all[0].ID != "also-early" || all[1].ID != "earlier" || all[2].ID != "later" {
		t.Fatalf("sort=%v", idsOf(all))
	}
	headers := store.List("header")
	if len(headers) != 2 {
		t.Fatalf("header filter=%v", idsOf(headers))
	}
	if !containsString([]string{"a", "b"}, "b") || containsString([]string{"a"}, "z") {
		t.Fatal("containsString")
	}
	if got := uniqueSources([]Call{{SessionSource: "header"}, {SessionSource: "header"}, {SessionSource: ""}, {SessionSource: "path"}}); len(got) != 2 {
		t.Fatalf("unique=%v", got)
	}
}

func TestMemoryStoreStateDefaultSessionAndClone(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	_, id := store.Begin("", Call{StartedAt: time.Now().UTC(), SessionSource: "default",
		Normalized: Normalized{ContentFilter: &ContentFilter{Categories: map[string]FilterResult{"hate": {Filtered: true}}}}})
	store.Finish("", id, func(c *Call) {
		c.EndedAt = time.Now().UTC()
		if c.Normalized.ContentFilter != nil {
			c.Normalized.ContentFilter.Raw = []json.RawMessage{json.RawMessage(`{"hate":{"filtered":true}}`)}
		}
	})
	sess, ok := store.Lookup(DefaultSessionID)
	if !ok || len(sess.Calls) != 1 {
		t.Fatalf("default session %+v ok=%v", sess, ok)
	}
	if sess.Calls[0].Normalized.ContentFilter == nil {
		t.Fatal("missing content filter clone")
	}
	sess.Calls[0].Normalized.ContentFilter.Categories["hate"] = FilterResult{}
	sess.Calls[0].Normalized.ContentFilter.Raw[0] = json.RawMessage(`{}`)
	again, ok := store.Lookup(DefaultSessionID)
	if !ok {
		t.Fatal("second lookup")
	}
	cf := again.Calls[0].Normalized.ContentFilter
	if cf == nil || !cf.Categories["hate"].Filtered || string(cf.Raw[0]) != `{"hate":{"filtered":true}}` {
		t.Fatalf("lookup result aliases store state: %+v", cf)
	}
	store.Delete(DefaultSessionID)
	if _, ok := store.Lookup(DefaultSessionID); ok {
		t.Fatal("deleted")
	}
}

func idsOf(in []SessionSummary) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = s.ID
	}
	return out
}

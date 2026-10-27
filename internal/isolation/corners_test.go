// SPDX-License-Identifier: AGPL-3.0-only

package isolation

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func chatCall(id, session string, seq int, users ...string) Call {
	msgs := make([]any, 0, len(users))
	for _, u := range users {
		msgs = append(msgs, map[string]any{"role": "user", "content": u})
	}
	raw, err := json.Marshal(map[string]any{"model": "gpt-4.1-nano", "messages": msgs})
	if err != nil {
		panic(err)
	}
	return Call{ID: id, SessionID: session, Seq: seq, RawRequest: raw}
}

func TestCheckEmptyOtherCorners(t *testing.T) {
	x := Session{ID: "sess-x", Calls: []Call{chatCall("call_x1", "sess-x", 1, "Where is my order?")}}

	t.Run("unknown_y_fails_and_names_id", func(t *testing.T) {
		ok, msg := Check(x, Session{ID: "never-created"}, Options{})
		if ok || !strings.Contains(msg, "never-created") || !strings.Contains(msg, "both sides") {
			t.Fatalf("ok=%v msg=%q", ok, msg)
		}
	})
	t.Run("typo_y_fails_and_names_typo", func(t *testing.T) {
		ok, msg := Check(x, Session{ID: "sess-x-typo"}, Options{})
		if ok || !strings.Contains(msg, "sess-x-typo") {
			t.Fatalf("ok=%v msg=%q", ok, msg)
		}
	})
	t.Run("existing_empty_y_fails", func(t *testing.T) {
		ok, msg := Check(x, Session{ID: "sess-unused", Calls: []Call{}}, Options{})
		if ok || !strings.Contains(msg, "sess-unused") {
			t.Fatalf("ok=%v msg=%q", ok, msg)
		}
	})
	t.Run("allow_empty_other_passes", func(t *testing.T) {
		ok, msg := Check(x, Session{ID: "never-created"}, Options{AllowEmpty: true})
		if !ok || msg != "" {
			t.Fatalf("ok=%v msg=%q", ok, msg)
		}
	})
	t.Run("empty_x_still_wins_over_empty_y", func(t *testing.T) {
		ok, msg := Check(Session{ID: "sess-x"}, Session{ID: "sess-y"}, Options{})
		if ok || !strings.Contains(msg, "sess-x") || strings.Contains(msg, "both sides") {
			t.Fatalf("empty X should use rule 4, got ok=%v msg=%q", ok, msg)
		}
	})
	t.Run("both_empty_allow_empty_passes", func(t *testing.T) {
		ok, msg := Check(Session{ID: "sess-x"}, Session{ID: "sess-y"}, Options{AllowEmpty: true})
		if !ok || msg != "" {
			t.Fatalf("ok=%v msg=%q", ok, msg)
		}
	})
}

func TestIntroducedLaterCallAllNewMessages(t *testing.T) {
	x := Session{ID: "sess-x", Calls: []Call{
		chatCall("call_x1", "sess-x", 1, "Opening question number one"),
		chatCall("call_x2", "sess-x", 2, "Opening question number one", "Second message sent by P itself", "Third message sent by P itself"),
	}}
	y := Session{ID: "sess-y", Calls: []Call{
		chatCall("call_y1", "sess-y", 1, "Second message sent by P itself"),
	}}
	ok, msg := Check(x, y, Options{})
	if !ok {
		t.Fatalf("own second message must not be a leak: %s", msg)
	}
}

func TestReplayedForeignTurnStillFails(t *testing.T) {
	x := Session{ID: "sess-x", Calls: []Call{
		chatCall("call_x1", "sess-x", 1, "Secret thread from the other app", "Continue from the saved thread."),
	}}
	y := Session{ID: "sess-y", Calls: []Call{
		chatCall("call_y1", "sess-y", 1, "Secret thread from the other app"),
	}}
	ok, msg := Check(x, y, Options{})
	if ok || !strings.Contains(msg, "also introduced by session sess-y") {
		t.Fatalf("want leak, ok=%v msg=%q", ok, msg)
	}
}

func TestCheckIgnoreAndMinChars(t *testing.T) {
	x := Session{ID: "sess-x", Calls: []Call{
		chatCall("call_x1", "sess-x", 1, "Hi", "Continue from the saved thread."),
	}}
	y := Session{ID: "sess-y", Calls: []Call{
		chatCall("call_y1", "sess-y", 1, "Hi"),
	}}
	ok, msg := Check(x, y, Options{MinChars: 8})
	if !ok {
		t.Fatalf("short Hi must be ignored: %s", msg)
	}
	ok, msg = Check(x, y, Options{MinChars: 1})
	if ok {
		t.Fatalf("min_chars=1 should leak Hi, msg=%q", msg)
	}
	x2 := Session{ID: "sess-x", Calls: []Call{
		chatCall("call_x1", "sess-x", 1, "Please ignore this canned leak", "Continue from the saved thread."),
	}}
	y2 := Session{ID: "sess-y", Calls: []Call{
		chatCall("call_y1", "sess-y", 1, "Please ignore this canned leak"),
	}}
	ok, msg = Check(x2, y2, Options{IgnoreTexts: []string{"Please ignore this canned leak"}})
	if !ok {
		t.Fatalf("ignore_texts should drop the leak: %s", msg)
	}
	ok, _ = Check(x2, y2, Options{MinChars: 0})
	if ok {
		t.Fatalf("default min_chars after 0 should still apply and leak")
	}
}

func TestCheckMalformedAndBase64Request(t *testing.T) {
	plain := []byte(`{"messages":[{"role":"user","content":"Where is my order?"}]}`)
	b64 := []byte(`"` + base64.StdEncoding.EncodeToString(plain) + `"`)
	x := Session{ID: "sess-x", Calls: []Call{{
		ID: "call_x1", SessionID: "sess-x", Seq: 1, RawRequest: json.RawMessage(`not-json`),
	}}}
	y := Session{ID: "sess-y", Calls: []Call{{
		ID: "call_y1", SessionID: "sess-y", Seq: 1, RawRequest: b64,
	}}}
	ok, msg := Check(x, y, Options{})
	if !ok {
		t.Fatalf("malformed X request has no user turns to leak: %s", msg)
	}

	x2 := Session{ID: "sess-x", Calls: []Call{{
		ID: "call_x1", SessionID: "sess-x", Seq: 1,
		RawRequest: json.RawMessage(`{"messages":[{"role":"user","content":"Where is my order?"},{"role":"user","content":"Continue from the saved thread."}]}`),
	}}}
	ok, msg = Check(x2, y, Options{})
	if ok || !strings.Contains(msg, "also introduced by") {
		t.Fatalf("base64 Y intro should leak into X history, ok=%v msg=%q", ok, msg)
	}
}

func TestCheckSharedCallIDEmptyIDIgnored(t *testing.T) {
	x := Session{ID: "sess-x", Calls: []Call{chatCall("", "sess-x", 1, "Where is my order?")}}
	y := Session{ID: "sess-y", Calls: []Call{chatCall("", "sess-y", 1, "A different long enough question")}}
	ok, msg := Check(x, y, Options{})
	if !ok {
		t.Fatalf("empty call ids are not shared: %s", msg)
	}
}

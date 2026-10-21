// SPDX-License-Identifier: AGPL-3.0-only

package isolation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type vectorFile struct {
	ID          string   `json:"id"`
	AllowEmpty  bool     `json:"allow_empty"`
	MinChars    int      `json:"min_chars"`
	IgnoreTexts []string `json:"ignore_texts"`
	X           Session  `json:"x"`
	Y           Session  `json:"y"`
	Expect      struct {
		Isolated         bool     `json:"isolated"`
		MessageContains  []string `json:"message_contains"`
	} `json:"expect"`
}

func TestIsolationVectors(t *testing.T) {
	files, err := filepath.Glob(vectorsDir() + "/*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no isolation vectors")
	}
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var v vectorFile
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		t.Run(v.ID, func(t *testing.T) {
			ok, msg := Check(v.X, v.Y, Options{
				AllowEmpty:  v.AllowEmpty,
				MinChars:    v.MinChars,
				IgnoreTexts: v.IgnoreTexts,
			})
			if ok != v.Expect.Isolated {
				t.Fatalf("isolated=%v want %v msg=%q", ok, v.Expect.Isolated, msg)
			}
			if v.Expect.Isolated && msg != "" {
				t.Fatalf("pass must have empty message, got %q", msg)
			}
			for _, frag := range v.Expect.MessageContains {
				if !strings.Contains(msg, frag) {
					t.Fatalf("message %q missing %q", msg, frag)
				}
			}
		})
	}
}

func vectorsDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "testdata", "isolation"))
}

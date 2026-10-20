// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestA1SpecVersionPin(t *testing.T) {
	cmd := exec.Command("go", "run", "./tools/specverify")
	cmd.Dir = repoRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("specverify: %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("sha256=")) {
		t.Fatalf("unexpected specverify output: %s", out)
	}
}

func TestA4OapiCodegenPinned(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	mod := string(raw)
	if !strings.Contains(mod, "tool github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen") {
		t.Fatal("go.mod must pin oapi-codegen via a tool directive")
	}
	if !strings.Contains(mod, "github.com/oapi-codegen/oapi-codegen/v2 v2.8.0") {
		t.Fatal("oapi-codegen must be pinned to v2.8.0")
	}
	err = filepath.WalkDir(repoRoot(t), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		name := filepath.Base(path)
		if name == "go.mod" || name == "Makefile" || strings.HasSuffix(name, ".md") || strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".sh") {
			body, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			if bytes.Contains(body, []byte("oapi-codegen@latest")) || bytes.Contains(body, []byte("oapi-codegen/v2/cmd/oapi-codegen@latest")) {
				t.Errorf("%s uses oapi-codegen@latest", path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestA5CompileTimeAssertionPresent(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "internal", "proxy", "handler_openai.go"))
	if err != nil {
		t.Fatal(err)
	}
	want := "var _ openaiapi.StrictServerInterface = (*openAIHandlers)(nil)"
	if !bytes.Contains(raw, []byte(want)) {
		t.Fatalf("missing compile-time assertion %s", want)
	}
	if bytes.Contains(raw, []byte("Unimplemented")) && bytes.Contains(raw, []byte("struct {")) {
		if bytes.Contains(raw, []byte("openaiapi.Unimplemented")) || bytes.Contains(raw, []byte("UnimplementedStrictServer")) {
			t.Fatal("handler_openai.go must not embed Unimplemented stubs")
		}
	}
	ops, err := os.ReadFile(filepath.Join(repoRoot(t), "api", "openai", "operations.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(ops, []byte("createChatCompletion")) || !bytes.Contains(ops, []byte("createResponse")) {
		t.Fatal("operations.txt must list createChatCompletion and createResponse")
	}
	overlay, err := os.ReadFile(filepath.Join(repoRoot(t), "api", "openai", "overlay.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(overlay, []byte("no-op marker")) {
		t.Fatal("overlay must not keep the ResponseStreamEvent no-op marker; mapping comes from specfilter")
	}
	filter, err := os.ReadFile(filepath.Join(repoRoot(t), "tools", "specfilter", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(filter, []byte(`"CreateEmbeddingResponse"`)) {
		t.Fatal("specfilter must rename CreateEmbeddingResponse.object so adding createEmbedding generates a clean missing-method error")
	}
}

func TestA7NoHandwrittenChoicesTags(t *testing.T) {
	dir := filepath.Join(repoRoot(t), "internal", "proxy")
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if !bytes.Contains(raw, []byte(`json:"choices"`)) {
			return nil
		}
		if filepath.Base(path) == "extensions.go" {
			return nil
		}
		t.Errorf("%s still has a handwritten json:\"choices\" tag", path)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

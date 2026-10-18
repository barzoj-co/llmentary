// SPDX-License-Identifier: AGPL-3.0-only

package specvalidate

import (
	"strconv"
	"strings"
	"unicode"
)

// PointerToPath converts a JSON Pointer to OpenAI's dotted/bracket form:
// /tools/0/function/parameters → tools[0].function.parameters
func PointerToPath(pointer string) string {
	pointer = strings.TrimSpace(pointer)
	if pointer == "" || pointer == "/" {
		return ""
	}
	if !strings.HasPrefix(pointer, "/") {
		pointer = "/" + pointer
	}
	parts := strings.Split(pointer, "/")[1:]
	var b strings.Builder
	for _, part := range parts {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		if part == "" {
			continue
		}
		if isIndex(part) {
			b.WriteByte('[')
			b.WriteString(part)
			b.WriteByte(']')
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('.')
		}
		b.WriteString(part)
	}
	return b.String()
}

func isIndex(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func joinPointer(base, key string) string {
	key = strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
	if base == "" || base == "/" {
		return "/" + key
	}
	return strings.TrimRight(base, "/") + "/" + key
}

func pointerIndex(base string, i int) string {
	return joinPointer(base, strconv.Itoa(i))
}

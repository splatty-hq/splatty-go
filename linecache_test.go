package splatty

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSource(t *testing.T, name string, lines int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	body := make([]string, lines)
	for i := range body {
		body[i] = fmt.Sprintf("line %d", i+1)
	}
	if err := os.WriteFile(path, []byte(strings.Join(body, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func TestLineCacheContextReturnsSurroundingLines(t *testing.T) {
	cache := &lineCache{}
	path := writeSource(t, "sample.go", 10)

	pre, line, post, ok := cache.context(path, 5, 2)
	if !ok {
		t.Fatal("context not found")
	}
	if want := []string{"line 3", "line 4"}; !equalStrings(pre, want) {
		t.Errorf("pre = %v, want %v", pre, want)
	}
	if want := "line 5"; line != want {
		t.Errorf("line = %q, want %q", line, want)
	}
	if want := []string{"line 6", "line 7"}; !equalStrings(post, want) {
		t.Errorf("post = %v, want %v", post, want)
	}
}

func TestLineCacheClampsAtFileBoundaries(t *testing.T) {
	cache := &lineCache{}
	path := writeSource(t, "sample.go", 10)

	pre, line, post, ok := cache.context(path, 1, 3)
	if !ok {
		t.Fatal("context not found at first line")
	}
	if len(pre) != 0 {
		t.Errorf("pre = %v, want empty", pre)
	}
	if line != "line 1" {
		t.Errorf("line = %q, want %q", line, "line 1")
	}
	if want := []string{"line 2", "line 3", "line 4"}; !equalStrings(post, want) {
		t.Errorf("post = %v, want %v", post, want)
	}

	pre, line, post, ok = cache.context(path, 10, 3)
	if !ok {
		t.Fatal("context not found at last line")
	}
	if want := []string{"line 7", "line 8", "line 9"}; !equalStrings(pre, want) {
		t.Errorf("pre = %v, want %v", pre, want)
	}
	if line != "line 10" {
		t.Errorf("line = %q, want %q", line, "line 10")
	}
	if len(post) != 0 {
		t.Errorf("post = %v, want empty", post)
	}
}

func TestLineCacheRejectsUnreadableAndOutOfRange(t *testing.T) {
	cache := &lineCache{}
	path := writeSource(t, "sample.go", 10)

	cases := []struct {
		name         string
		path         string
		lineno       int
		contextLines int
	}{
		{"missing file", filepath.Join(filepath.Dir(path), "nope.go"), 3, 2},
		{"directory", filepath.Dir(path), 3, 2},
		{"empty path", "", 3, 2},
		{"past end of file", path, 99, 2},
		{"line zero", path, 0, 2},
		{"context disabled", path, 3, 0},
	}
	for _, tc := range cases {
		if _, _, _, ok := cache.context(tc.path, tc.lineno, tc.contextLines); ok {
			t.Errorf("%s: got context, want none", tc.name)
		}
	}
}

func TestLineCachePicksUpFileChanges(t *testing.T) {
	cache := &lineCache{}
	path := writeSource(t, "sample.go", 10)

	if _, line, _, _ := cache.context(path, 5, 1); line != "line 5" {
		t.Fatalf("line = %q, want %q", line, "line 5")
	}

	body := make([]string, 10)
	for i := range body {
		body[i] = fmt.Sprintf("changed %d", i+1)
	}
	if err := os.WriteFile(path, []byte(strings.Join(body, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, line, _, _ := cache.context(path, 5, 1); line != "changed 5" {
		t.Errorf("line = %q, want %q", line, "changed 5")
	}
}

func TestLineCacheTruncatesLongLinesAndSkipsBigFiles(t *testing.T) {
	cache := &lineCache{}
	dir := t.TempDir()

	long := filepath.Join(dir, "long.go")
	if err := os.WriteFile(long, []byte(strings.Repeat("x", maxSourceLineLength+500)+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, line, _, ok := cache.context(long, 1, 1)
	if !ok {
		t.Fatal("context not found")
	}
	if len(line) != maxSourceLineLength {
		t.Errorf("len(line) = %d, want %d", len(line), maxSourceLineLength)
	}

	big := filepath.Join(dir, "big.go")
	if err := os.WriteFile(big, []byte(strings.Repeat("a\n", maxSourceFileBytes)), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, _, _, ok := cache.context(big, 1, 1); ok {
		t.Error("got context for an oversized file, want none")
	}
}

func TestLineCacheKeepsPayloadEncodable(t *testing.T) {
	cache := &lineCache{}
	path := filepath.Join(t.TempDir(), "invalid.go")
	if err := os.WriteFile(path, []byte("caf\xc3\x28 := 1\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, line, _, ok := cache.context(path, 1, 1)
	if !ok {
		t.Fatal("context not found")
	}
	if !strings.Contains(line, "caf") {
		t.Errorf("line = %q, want it to keep the valid bytes", line)
	}
	for _, r := range line {
		if r == 0xFFFD {
			t.Errorf("line = %q, want invalid bytes dropped", line)
		}
	}
}

func TestLineCacheBoundsTheNumberOfCachedFiles(t *testing.T) {
	cache := &lineCache{}
	dir := t.TempDir()
	for i := 0; i <= maxCachedSourceFiles; i++ {
		path := filepath.Join(dir, fmt.Sprintf("f%d.go", i))
		if err := os.WriteFile(path, []byte("line 1\n"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if _, _, _, ok := cache.context(path, 1, 1); !ok {
			t.Fatalf("context not found for %s", path)
		}
	}

	cache.mu.Lock()
	size := len(cache.files)
	cache.mu.Unlock()
	if size > maxCachedSourceFiles {
		t.Errorf("cached files = %d, want at most %d", size, maxCachedSourceFiles)
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

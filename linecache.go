package splatty

import (
	"os"
	"strings"
	"sync"
)

const (
	// defaultContextLines is how many source lines flank a frame by default.
	defaultContextLines = 5
	// maxCachedSourceFiles bounds the cache; reaching it drops everything
	// rather than tracking recency, which keeps the bookkeeping to one map.
	maxCachedSourceFiles = 100
	// maxSourceFileBytes skips generated or vendored blobs masquerading as
	// source.
	maxSourceFileBytes = 512 * 1024
	// maxSourceLineLength keeps a minified line from dominating the payload.
	maxSourceLineLength = 1000
)

type cachedSource struct {
	modTime int64
	size    int64
	lines   []string
}

// lineCache serves source lines for stack frames. Entries are keyed by
// modification time and size, so a file edited under a long-lived process is
// re-read rather than served stale.
type lineCache struct {
	mu    sync.Mutex
	files map[string]cachedSource
}

var sourceCache = &lineCache{}

// context returns the lines around lineno, and whether the file could be read
// at all. Callers leave the frame untouched when ok is false.
func (c *lineCache) context(path string, lineno, contextLines int) (pre []string, line string, post []string, ok bool) {
	if path == "" || lineno < 1 || contextLines < 1 {
		return nil, "", nil, false
	}

	lines := c.linesFor(path)
	index := lineno - 1
	if index >= len(lines) {
		return nil, "", nil, false
	}

	start := index - contextLines
	if start < 0 {
		start = 0
	}
	end := index + contextLines + 1
	if end > len(lines) {
		end = len(lines)
	}

	return append([]string(nil), lines[start:index]...),
		lines[index],
		append([]string(nil), lines[index+1:end]...),
		true
}

func (c *lineCache) linesFor(path string) []string {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil
	}
	if info.Size() == 0 || info.Size() > maxSourceFileBytes {
		return nil
	}

	modTime := info.ModTime().UnixNano()

	c.mu.Lock()
	defer c.mu.Unlock()

	if entry, hit := c.files[path]; hit && entry.modTime == modTime && entry.size == info.Size() {
		return entry.lines
	}
	if len(c.files) >= maxCachedSourceFiles {
		c.files = nil
	}
	if c.files == nil {
		c.files = make(map[string]cachedSource, maxCachedSourceFiles)
	}

	lines := readSourceLines(path)
	c.files[path] = cachedSource{modTime: modTime, size: info.Size(), lines: lines}
	return lines
}

func (c *lineCache) reset() {
	c.mu.Lock()
	c.files = nil
	c.mu.Unlock()
}

func readSourceLines(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	raw := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if n := len(raw); n > 0 && raw[n-1] == "" {
		raw = raw[:n-1]
	}

	lines := make([]string, len(raw))
	for i, line := range raw {
		if len(line) > maxSourceLineLength {
			line = line[:maxSourceLineLength]
		}
		// Truncation can halve a rune, and a source file is not guaranteed to
		// be UTF-8 at all; either way the payload has to stay encodable.
		lines[i] = strings.ToValidUTF8(line, "")
	}
	return lines
}

// addSourceContext fills in the source lines around every frame it can read.
// Frames whose file is gone, unreadable or too large keep their bare location.
func addSourceContext(frames []Frame, cfg *Config) {
	contextLines := cfg.contextLines()
	if contextLines < 1 {
		return
	}
	for i := range frames {
		pre, line, post, ok := sourceCache.context(frames[i].AbsPath, frames[i].Lineno, contextLines)
		if !ok {
			continue
		}
		frames[i].PreContext = pre
		frames[i].ContextLine = line
		frames[i].PostContext = post
	}
}

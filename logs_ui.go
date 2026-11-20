package main

import (
	"io"
	"os"
	"slices"
	"strings"
)

// readLogTail returns the last max bytes of the file at path as whole lines,
// newest first.
func readLogTail(path string, max int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	start := int64(0)
	if st, err := f.Stat(); err == nil && st.Size() > max {
		start = st.Size() - max
	}
	b, err := io.ReadAll(io.NewSectionReader(f, start, max))
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if start > 0 && len(lines) > 1 {
		lines = lines[1:] // the first line was cut mid-way
	}
	slices.Reverse(lines)
	return strings.Join(lines, "\n")
}

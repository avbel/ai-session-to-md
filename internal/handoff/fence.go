package handoff

import (
	"strings"
)

// longestBackticks is the length of the longest run of backticks in s.
func longestBackticks(s string) int {
	longest, cur := 0, 0
	for _, r := range s {
		if r == '`' {
			cur++
			continue
		}
		if cur > longest {
			longest = cur
		}
		cur = 0
	}
	if cur > longest {
		longest = cur
	}
	return longest
}

// fenceRun returns a backtick run strictly longer than any run in content,
// at least min backticks, so the content can never close its own fence.
func fenceRun(content string, min int) string {
	n := min
	if r := longestBackticks(content) + 1; r > n {
		n = r
	}
	return strings.Repeat("`", n)
}

// indentedFence wraps content in a fence indented two spaces under a list
// bullet. A trailing newline is added.
func indentedFence(content, lang string, min int) string {
	run := fenceRun(content, min)
	var b strings.Builder
	open := run + lang + "\n"
	b.WriteString("  " + open)
	for line := range strings.Lines(strings.TrimRight(content, "\n")) {
		if s := strings.TrimRight(line, "\n"); s != "" {
			b.WriteString("  " + s + "\n")
		} else {
			b.WriteString("\n")
		}
	}
	b.WriteString("  " + run + "\n")
	return b.String()
}

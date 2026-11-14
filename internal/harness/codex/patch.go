package codex

import (
	"encoding/json"
	"regexp"
	"strings"
)

// fileHeaders match the patch headers whose argument is a file path.
var (
	addUpdateDeleteRe = regexp.MustCompile(`(?m)^\*\*\* (?:Add|Update|Delete) File: (.+)$`)
	moveToRe          = regexp.MustCompile(`(?m)^\*\*\* Move to: (.+)$`)
)

// patchPaths collects the touched file paths from a tool call input of
// apply_patch, or of any input embedding a `*** Begin Patch` block. The
// patch may be the raw input, or a string field inside a JSON object
// (Codex wraps it as {"input": "*** Begin Patch…"}).
func patchPaths(name, input string) []string {
	if name != "apply_patch" && !strings.Contains(input, "*** Begin Patch") {
		return nil
	}

	candidates := []string{input}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(input), &obj); err == nil {
		for _, raw := range obj {
			var s string
			if err := json.Unmarshal(raw, &s); err == nil && strings.Contains(s, "Begin Patch") {
				candidates = append(candidates, s)
			}
		}
	}

	var paths []string
	for _, text := range candidates {
		paths = append(paths, headerPaths(text)...)
	}
	return paths
}

// headerPaths extracts paths from Add/Update/Delete File and Move to
// headers, trimming surrounding whitespace.
func headerPaths(text string) []string {
	var paths []string
	for _, re := range []*regexp.Regexp{addUpdateDeleteRe, moveToRe} {
		for _, m := range re.FindAllStringSubmatch(text, -1) {
			if p := strings.TrimSpace(m[1]); p != "" {
				paths = append(paths, p)
			}
		}
	}
	return paths
}

package opencode

import (
	"bytes"
	"cmp"
	"encoding/json"
	"regexp"
	"strings"
)

// fileTools are the OpenCode tool names whose calls touch files.
var fileTools = map[string]struct{}{
	"edit":        {},
	"write":       {},
	"patch":       {},
	"apply_patch": {},
	"multiedit":   {},
}

// patchHeader matches the unified-patch headers that name a target path.
var patchHeader = regexp.MustCompile(`(?m)^\*\*\* (?:Add File|Update File|Delete File|Move to): (.+?)\s*$`)

func isFileTool(name string) bool {
	_, ok := fileTools[strings.ToLower(name)]
	return ok
}

// noteFiles records file paths from a tool call: input filePath/path, plus
// patch headers from patchText (or from a bare-string patch input).
func (b *builder) noteFiles(item contentItem) {
	if !isFileTool(item.Name) {
		return
	}
	var fields struct {
		FilePath  string `json:"filePath"`
		Path      string `json:"path"`
		PatchText string `json:"patchText"`
	}
	_ = json.Unmarshal(item.State.Input, &fields)
	b.note(fields.FilePath)
	b.note(fields.Path)
	patch := cmp.Or(fields.PatchText, jsonString(item.State.Input))
	for _, p := range patchPaths(patch) {
		b.note(p)
	}
}

func (b *builder) note(path string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	if _, ok := b.seen[path]; ok {
		return
	}
	b.seen[path] = struct{}{}
	b.files = append(b.files, path)
}

func patchPaths(text string) []string {
	var out []string
	for _, m := range patchHeader.FindAllStringSubmatch(text, -1) {
		out = append(out, strings.TrimSpace(m[1]))
	}
	return out
}

// jsonString returns the value of a JSON string payload, "" otherwise.
func jsonString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}

// compactJSON renders raw as one line; a non-JSON payload passes through.
func compactJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return string(raw)
	}
	return buf.String()
}

// resultText extracts tool output: state.content as a string, or an array of
// {text} items joined with newlines; anything else is kept as-is.
func resultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var items []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &items) == nil {
		texts := make([]string, 0, len(items))
		for _, it := range items {
			texts = append(texts, it.Text)
		}
		return strings.Join(texts, "\n")
	}
	return string(raw)
}

// fileNames reads data.files[]: strings as-is, objects via their name, path,
// filename or url field.
func fileNames(raw json.RawMessage) []string {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return nil
	}
	var out []string
	for _, it := range items {
		var s string
		if json.Unmarshal(it, &s) == nil {
			out = append(out, s)
			continue
		}
		var obj struct {
			Name     string `json:"name"`
			Path     string `json:"path"`
			Filename string `json:"filename"`
			URL      string `json:"url"`
		}
		if json.Unmarshal(it, &obj) == nil {
			if name := cmp.Or(obj.Name, obj.Path, obj.Filename, obj.URL); name != "" {
				out = append(out, name)
			}
		}
	}
	return out
}

// modelJSON renders session_v2.model — a JSON {"id","providerID"} object — as
// "providerID/id". A JSON string or a plain string passes through.
func modelJSON(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var plain string
	if json.Unmarshal([]byte(raw), &plain) == nil {
		return plain
	}
	var obj struct {
		ID         string `json:"id"`
		ProviderID string `json:"providerID"`
	}
	if json.Unmarshal([]byte(raw), &obj) == nil && obj.ID != "" {
		if obj.ProviderID != "" {
			return obj.ProviderID + "/" + obj.ID
		}
		return obj.ID
	}
	return raw
}

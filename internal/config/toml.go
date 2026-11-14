package config

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// parseTOML reads the subset of TOML the config schema uses: comments, bare
// and quoted keys, [table] headers, basic and literal strings, integers,
// floats, booleans, single-line arrays and inline tables. Multi-line values
// are not supported.
func parseTOML(src string) (map[string]any, error) {
	doc := map[string]any{}
	table := doc
	for i, line := range strings.Split(src, "\n") {
		lineNo := i + 1
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			name, err := tableHeader(line, lineNo)
			if err != nil {
				return nil, err
			}
			tbl := doc
			for _, part := range name {
				next, ok := tbl[part].(map[string]any)
				if !ok {
					next = map[string]any{}
					tbl[part] = next
				}
				tbl = next
			}
			table = tbl
			continue
		}
		key, rest, err := splitKeyValue(line, lineNo)
		if err != nil {
			return nil, err
		}
		val, rest2, err := parseValue(rest, lineNo)
		if err != nil {
			return nil, err
		}
		if rest := strings.TrimSpace(rest2); rest != "" && !strings.HasPrefix(rest, "#") {
			return nil, fmt.Errorf("line %d: trailing content %q", lineNo, rest)
		}
		table[key] = val
	}
	return doc, nil
}

func tableHeader(line string, lineNo int) ([]string, error) {
	if !strings.HasSuffix(line, "]") {
		return nil, fmt.Errorf("line %d: malformed table header", lineNo)
	}
	return splitKey(line[1:len(line)-1], lineNo)
}

// splitKeyValue splits "key = value" at the first '='; keys may be quoted.
func splitKeyValue(line string, lineNo int) (string, string, error) {
	key, rest, ok := strings.Cut(line, "=")
	if !ok {
		return "", "", fmt.Errorf("line %d: expected key = value", lineNo)
	}
	parts, err := splitKey(key, lineNo)
	if err != nil {
		return "", "", err
	}
	if len(parts) != 1 {
		return "", "", fmt.Errorf("line %d: dotted keys are not supported", lineNo)
	}
	return parts[0], strings.TrimSpace(rest), nil
}

// splitKey parses a bare or quoted key, optionally dotted (for headers).
func splitKey(s string, lineNo int) ([]string, error) {
	var out []string
	for part := range strings.SplitSeq(s, ".") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("line %d: empty key", lineNo)
		}
		if quoted, ok := unquote(part); ok {
			out = append(out, quoted)
			continue
		}
		if !isBareKey(part) {
			return nil, fmt.Errorf("line %d: invalid key %q", lineNo, part)
		}
		out = append(out, part)
	}
	return out, nil
}

func isBareKey(s string) bool {
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' {
			return false
		}
	}
	return len(s) > 0
}

// unquote recognizes a fully quoted string token and decodes it.
func unquote(s string) (string, bool) {
	if len(s) >= 2 && s[0] == '\'' {
		if s[len(s)-1] != '\'' {
			return "", false
		}
		return s[1 : len(s)-1], true
	}
	if len(s) >= 2 && s[0] == '"' {
		if s[len(s)-1] != '"' {
			return "", false
		}
		v, err := basicString(s[1 : len(s)-1])
		if err != nil {
			return "", false
		}
		return v, true
	}
	return "", false
}

func basicString(body string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(body); i++ {
		c := body[i]
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		i++
		if i >= len(body) {
			return "", fmt.Errorf("dangling escape")
		}
		switch body[i] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case '"':
			b.WriteByte('"')
		case '\\':
			b.WriteByte('\\')
		case 'u', 'U':
			n := 4
			if body[i] == 'U' {
				n = 8
			}
			if i+n >= len(body) {
				return "", fmt.Errorf("short unicode escape")
			}
			code, err := strconv.ParseUint(body[i+1:i+1+n], 16, 32)
			if err != nil {
				return "", fmt.Errorf("bad unicode escape: %v", err)
			}
			if code > unicode.MaxRune {
				return "", fmt.Errorf("unicode escape out of range")
			}
			b.WriteRune(rune(code))
			i += n
		default:
			return "", fmt.Errorf("unknown escape \\%c", body[i])
		}
	}
	return b.String(), nil
}

// parseValue parses one TOML value and returns the remainder of the line.
func parseValue(s string, lineNo int) (any, string, error) {
	s = strings.TrimLeft(s, " \t")
	if s == "" {
		return nil, "", fmt.Errorf("line %d: missing value", lineNo)
	}
	switch s[0] {
	case '"', '\'':
		end := stringEnd(s)
		if end < 0 {
			return nil, "", fmt.Errorf("line %d: unterminated string", lineNo)
		}
		v, ok := unquote(s[:end])
		if !ok {
			return nil, "", fmt.Errorf("line %d: bad string", lineNo)
		}
		return v, s[end:], nil
	case '[':
		return parseArray(s, lineNo)
	case '{':
		return parseInlineTable(s, lineNo)
	}
	return parseBare(s, lineNo)
}

// stringEnd returns the index just past a quoted string token.
func stringEnd(s string) int {
	quote := s[0]
	for i := 1; i < len(s); i++ {
		if quote == '"' && s[i] == '\\' {
			i++
			continue
		}
		if s[i] == quote {
			return i + 1
		}
	}
	return -1
}

func parseArray(s string, lineNo int) (any, string, error) {
	rest := s[1:]
	var out []any
	for {
		rest = strings.TrimLeft(rest, " \t")
		if rest == "" {
			return nil, "", fmt.Errorf("line %d: unterminated array", lineNo)
		}
		if rest[0] == ']' {
			return out, rest[1:], nil
		}
		v, after, err := parseValue(rest, lineNo)
		if err != nil {
			return nil, "", err
		}
		out = append(out, v)
		rest = strings.TrimLeft(after, " \t")
		if rest == "" {
			return nil, "", fmt.Errorf("line %d: unterminated array", lineNo)
		}
		if rest[0] == ',' {
			rest = rest[1:]
			continue
		}
		if rest[0] != ']' {
			return nil, "", fmt.Errorf("line %d: expected , or ] in array", lineNo)
		}
	}
}

func parseInlineTable(s string, lineNo int) (any, string, error) {
	rest := s[1:]
	out := map[string]any{}
	for {
		rest = strings.TrimLeft(rest, " \t")
		if rest == "" {
			return nil, "", fmt.Errorf("line %d: unterminated inline table", lineNo)
		}
		if rest[0] == '}' {
			return out, rest[1:], nil
		}
		eq := strings.Index(rest, "=")
		if eq < 0 {
			return nil, "", fmt.Errorf("line %d: bad inline table entry", lineNo)
		}
		parts, err := splitKey(rest[:eq], lineNo)
		if err != nil || len(parts) != 1 {
			return nil, "", fmt.Errorf("line %d: bad inline table key", lineNo)
		}
		val, after, err := parseValue(rest[eq+1:], lineNo)
		if err != nil {
			return nil, "", err
		}
		out[parts[0]] = val
		rest = strings.TrimLeft(after, " \t")
		if rest == "" {
			return nil, "", fmt.Errorf("line %d: unterminated inline table", lineNo)
		}
		if rest[0] == ',' {
			rest = rest[1:]
			continue
		}
		if rest[0] != '}' {
			return nil, "", fmt.Errorf("line %d: expected , or } in inline table", lineNo)
		}
	}
}

func parseBare(s string, lineNo int) (any, string, error) {
	end := len(s)
	for i := range s {
		if s[i] == ',' || s[i] == '}' || s[i] == ']' || s[i] == '#' {
			end = i
			break
		}
	}
	tok := strings.TrimSpace(s[:end])
	switch tok {
	case "true":
		return true, s[end:], nil
	case "false":
		return false, s[end:], nil
	}
	if n, err := strconv.ParseInt(tok, 10, 64); err == nil {
		return n, s[end:], nil
	}
	if f, err := strconv.ParseFloat(tok, 64); err == nil {
		return f, s[end:], nil
	}
	return nil, "", fmt.Errorf("line %d: unsupported value %q", lineNo, tok)
}

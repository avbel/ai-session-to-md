package claudecode

import (
	"log/slog"
	"slices"
)

// activeChain returns the indexes of the user and assistant entries on the
// active branch, oldest first. It handles rewinds/forks by picking the last
// non-sidechain user/assistant entry as the leaf and walking parentUuid
// upward through entries of any type; a compact boundary continues through
// logicalParentUuid. Fewer than two emitted entries falls back to file
// order, with a warning.
func activeChain(lines []line) []int {
	index := uuidIndex(lines)
	leaf := -1
	for i, ln := range lines {
		if !ln.IsSidechain && (ln.Type == typeUser || ln.Type == typeAssistant) {
			leaf = i
		}
	}
	if leaf < 0 {
		return nil
	}
	var chain []int
	visited := make(map[int]bool, len(lines))
	cur := leaf
	for !visited[cur] {
		visited[cur] = true
		chain = append(chain, cur)
		next := lines[cur].parentRef()
		if next == "" {
			break
		}
		parent, ok := index[next]
		if !ok {
			break
		}
		cur = parent
	}
	slices.Reverse(chain)
	var out []int
	for _, i := range chain {
		if lines[i].Type == typeUser || lines[i].Type == typeAssistant {
			out = append(out, i)
		}
	}
	if len(out) >= 2 {
		return out
	}
	slog.Warn("claude-code: active branch too short; falling back to file order", "emitted", len(out))
	return fallbackOrder(lines)
}

// parentRef is the uuid this entry continues from. A compact boundary has a
// null parentUuid and points at its pre-compaction ancestor through
// logicalParentUuid instead.
func (l line) parentRef() string {
	if l.ParentUUID == "" && l.Type == typeSystem && l.Subtype == "compact_boundary" {
		return l.LogicalParentUUID
	}
	return l.ParentUUID
}

// uuidIndex maps every line uuid, of any type, to its first index; a third
// of user/assistant parent links point at attachment entries in real data.
func uuidIndex(lines []line) map[string]int {
	index := make(map[string]int, len(lines))
	for i, ln := range lines {
		if ln.UUID == "" {
			continue
		}
		if _, dup := index[ln.UUID]; !dup {
			index[ln.UUID] = i
		}
	}
	return index
}

func fallbackOrder(lines []line) []int {
	var out []int
	for i, ln := range lines {
		if !ln.IsSidechain && (ln.Type == typeUser || ln.Type == typeAssistant) {
			out = append(out, i)
		}
	}
	return out
}

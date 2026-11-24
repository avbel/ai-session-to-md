package antigravity

import (
	"bufio"
	"io"
	"os"
	"regexp"

	"github.com/avbel/ai-session-to-md/internal/session"
)

// maxTaskFileBytes caps the read; the checklist is small and a runaway file
// must not stall the load.
const maxTaskFileBytes = 1 << 20

// taskLine matches one checklist item of agy's task.md artifact, with or
// without backticks around the box: "- `[x]` Done", "- [/] Doing", "- [ ] Todo".
var taskLine = regexp.MustCompile("^\\s*[-*]\\s+`?\\[([ xX/])\\]`?\\s+(.+?)\\s*$")

// readTasks parses the checklist agy keeps in the conversation's brain dir;
// a missing or unreadable file yields no todos.
func readTasks(path string) []session.Todo {
	f, err := os.Open(path) //nolint:gosec // G304: path is built from the validated conversation id
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	return parseTasks(bufio.NewScanner(io.LimitReader(f, maxTaskFileBytes)))
}

func parseTasks(sc *bufio.Scanner) []session.Todo {
	var todos []session.Todo
	for sc.Scan() {
		m := taskLine.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		todos = append(todos, session.Todo{Content: m[2], Status: taskStatus(m[1])})
	}
	return todos
}

func taskStatus(box string) string {
	switch box {
	case "x", "X":
		return "completed"
	case "/":
		return "in_progress"
	default:
		return "pending"
	}
}

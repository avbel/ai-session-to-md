package session

import "time"

type Harness string

const (
	HarnessClaudeCode  Harness = "claude-code"
	HarnessCodex       Harness = "codex"
	HarnessOpenCode    Harness = "opencode"
	HarnessAntigravity Harness = "antigravity"
)

type Meta struct {
	Harness   Harness
	ID        string
	Title     string
	Cwd       string
	Branch    string
	UpdatedAt time.Time
	Archived  bool
	Source    string
}

type TurnKind int

const (
	UserText TurnKind = iota
	AssistantText
	Reasoning
	ToolCall
	ToolResult
	CompactionSummary
	Interrupted
)

func (k TurnKind) String() string {
	switch k {
	case UserText:
		return "user"
	case AssistantText:
		return "assistant"
	case Reasoning:
		return "reasoning"
	case ToolCall:
		return "tool_call"
	case ToolResult:
		return "tool_result"
	case CompactionSummary:
		return "compaction_summary"
	case Interrupted:
		return "interrupted"
	default:
		return "unknown"
	}
}

type Turn struct {
	Kind     TurnKind
	Time     time.Time
	Text     string
	ToolName string
	ToolID   string
	Input    string
	IsError  bool
}

type Todo struct {
	Content string
	Status  string
}

type GitRecord struct {
	RepoURL string
	Branch  string
	Commit  string
}

type Session struct {
	Meta
	Model        string
	HarnessVer   string
	RecordedGit  GitRecord
	Turns        []Turn
	Todos        []Todo
	FilesTouched []string
	Skipped      int
}

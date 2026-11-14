package main

import (
	"encoding/json"

	"github.com/avbel/ai-session-to-md/internal/gitinfo"
	"github.com/avbel/ai-session-to-md/internal/reduce"
	"github.com/avbel/ai-session-to-md/internal/session"
)

// maxFactMessage caps LastUser/LastAssistant in the facts JSON.
const maxFactMessage = 4000

// statusFacts says where the session stopped.
type statusFacts struct {
	Text        string       `json:"text"`
	PendingTool *pendingTool `json:"pending_tool,omitempty"`
	Finish      string       `json:"finish,omitempty"`
	MidTurn     bool         `json:"mid_turn,omitempty"`
}

type pendingTool struct {
	Name  string `json:"name,omitempty"`
	Input string `json:"input,omitempty"`
}

type gitFacts struct {
	Source          string   `json:"source"`
	RepoURL         string   `json:"repo_url,omitempty"`
	Branch          string   `json:"branch,omitempty"`
	Head            string   `json:"head,omitempty"`
	HeadSubject     string   `json:"head_subject,omitempty"`
	Ahead           int      `json:"ahead,omitempty"`
	Behind          int      `json:"behind,omitempty"`
	UpstreamMissing bool     `json:"upstream_missing,omitempty"`
	StatusLines     []string `json:"status_lines,omitempty"`
	DiffStatSummary string   `json:"diffstat_summary,omitempty"`
	DiffStatFiles   []string `json:"diffstat_files,omitempty"`
}

// factsDoc is the <session_facts> JSON: authoritative facts extracted by
// code, per the system prompt. Every string is redacted before it lands here.
type factsDoc struct {
	Harness       string         `json:"harness"`
	SessionID     string         `json:"session_id"`
	Title         string         `json:"title,omitempty"`
	Model         string         `json:"model,omitempty"`
	Status        statusFacts    `json:"status"`
	LastUser      string         `json:"last_user_message,omitempty"`
	LastAssistant string         `json:"last_assistant_message,omitempty"`
	Todos         []session.Todo `json:"todos,omitempty"`
	Git           gitFacts       `json:"git"`
	FilesTouched  []string       `json:"files_touched,omitempty"`
	Skipped       int            `json:"skipped_lines,omitempty"`
}

// buildFacts renders the redacted session, status and git state into the
// compact facts JSON.
func buildFacts(meta session.Meta, s session.Session, status session.Status, git gitinfo.Result, rd *redactor) string {
	doc := factsDoc{
		Harness:   string(meta.Harness),
		SessionID: meta.ID,
		Title:     s.Title,
		Model:     s.Model,
		Status: statusFacts{
			Text:    status.Text,
			Finish:  status.Finish,
			MidTurn: status.MidTurn,
		},
		LastUser:      reduce.CapHeadTail(status.LastUser, maxFactMessage),
		LastAssistant: reduce.CapHeadTail(status.LastAssistant, maxFactMessage),
		Todos:         s.Todos,
		Skipped:       s.Skipped,
	}
	if status.PendingToolName != "" {
		doc.Status.PendingTool = &pendingTool{Name: status.PendingToolName, Input: status.PendingToolInput}
	}
	doc.Git = gitFacts{
		Source:          git.Source,
		RepoURL:         rd.str(git.RepoURL),
		Branch:          git.Branch,
		Head:            git.Head,
		HeadSubject:     rd.str(git.HeadSubject),
		Ahead:           git.Ahead,
		Behind:          git.Behind,
		UpstreamMissing: git.UpstreamMissing,
		DiffStatSummary: rd.str(git.DiffStatSummary),
	}
	for _, line := range git.StatusLines {
		doc.Git.StatusLines = append(doc.Git.StatusLines, rd.str(line))
	}
	for _, f := range git.DiffStatFiles {
		doc.Git.DiffStatFiles = append(doc.Git.DiffStatFiles, rd.str(f))
	}
	doc.FilesTouched = touchedFiles(rd, s, git)
	b, err := json.Marshal(doc)
	if err != nil {
		return "{}"
	}
	return string(b)
}

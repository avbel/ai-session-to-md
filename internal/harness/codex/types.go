package codex

import (
	"encoding/json"
	"fmt"
	"iter"

	"github.com/avbel/ai-session-to-md/internal/jsonl"
	"github.com/avbel/ai-session-to-md/internal/session"
)

// envelope is the common shape of every rollout line.
type envelope struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

// metaPayload is the session_meta payload. Source may be a plain string or a
// JSON object such as {"subagent":…}; it stays raw for that reason.
type metaPayload struct {
	ID           string          `json:"id"`
	SessionID    string          `json:"session_id"`
	Cwd          string          `json:"cwd"`
	CliVersion   string          `json:"cli_version"`
	Source       json.RawMessage `json:"source"`
	ThreadSource string          `json:"thread_source"`
	Git          struct {
		RepositoryURL string `json:"repository_url"`
		Branch        string `json:"branch"`
		CommitHash    string `json:"commit_hash"`
	} `json:"git"`
}

type turnContextPayload struct {
	Model string `json:"model"`
	Cwd   string `json:"cwd"`
}

type compactedPayload struct {
	Message string `json:"message"`
}

type eventMsgPayload struct {
	Type string          `json:"type"`
	Item json.RawMessage `json:"item"`
}

// openRollout iterates a rollout file's lines, transparently decompressing
// .zst archives via the shared jsonl reader.
func openRollout(path string) (iter.Seq2[int, []byte], func(), error) {
	lines, closeFn, err := jsonl.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open rollout %s: %w", path, err)
	}
	return lines, closeFn, nil
}

// userPayloadThread applies the subagent filter to a session_meta payload:
// thread_source 'user' passes; empty thread_source passes only when the
// source is not a JSON object (subagent sources are objects).
func userPayloadThread(meta metaPayload) bool {
	switch meta.ThreadSource {
	case "user":
		return true
	case "":
		return !isJSONObject(meta.Source)
	default:
		return false
	}
}

func isJSONObject(raw json.RawMessage) bool {
	return len(raw) > 0 && raw[0] == '{'
}

func gitRecordFrom(meta metaPayload) session.GitRecord {
	return session.GitRecord{
		RepoURL: meta.Git.RepositoryURL,
		Branch:  meta.Git.Branch,
		Commit:  meta.Git.CommitHash,
	}
}

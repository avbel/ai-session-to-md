package codex

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/avbel/ai-session-to-md/internal/session"
)

// loadState accumulates everything a rollout stream produces.
type loadState struct {
	cwd       string
	model     string
	cliVer    string
	git       session.GitRecord
	turns     []session.Turn
	todos     []session.Todo
	files     []string
	skipped   int
	midTurn   bool
	openCalls []int // indices of unpaired ToolCall turns, oldest first
	now       time.Time
}

// Load parses one Codex session from its rollout file. DB-listed metas are
// re-resolved through the threads table; file-listed metas point at the
// rollout directly.
func (a *Adapter) Load(ctx context.Context, m session.Meta) (session.Session, error) {
	path, err := a.rolloutFor(ctx, m)
	if err != nil {
		return session.Session{}, err
	}

	s, err := a.parseRollout(path, m)
	if err != nil {
		return session.Session{}, fmt.Errorf("parse codex rollout %s: %w", path, err)
	}
	return s, nil
}

// rolloutFor maps a listed Meta to an existing rollout file path.
func (a *Adapter) rolloutFor(ctx context.Context, m session.Meta) (string, error) {
	if !strings.HasPrefix(m.Source, "db:") {
		return m.Source, nil
	}

	dbPath, id, err := splitDBSource(m.Source)
	if err != nil {
		return "", err
	}
	stored, err := threadRolloutPath(ctx, dbPath, id)
	if err != nil {
		return "", fmt.Errorf("read codex thread %s: %w", id, err)
	}
	path := a.resolveRollout(stored, id)
	if path == "" {
		return "", fmt.Errorf("rollout file for codex thread %s not found", id)
	}
	return path, nil
}

// splitDBSource splits the "db:<path>#<id>" diagnostic source.
func splitDBSource(source string) (string, string, error) {
	dbPath, id, ok := strings.Cut(strings.TrimPrefix(source, "db:"), "#")
	if !ok || dbPath == "" || id == "" {
		return "", "", fmt.Errorf("malformed codex source %q", source)
	}
	return dbPath, id, nil
}

// threadRolloutPath fetches the stored rollout_path for one thread.
func threadRolloutPath(ctx context.Context, dbPath, id string) (string, error) {
	db, err := sql.Open("sqlite", readOnlyDSN(dbPath))
	if err != nil {
		return "", fmt.Errorf("open codex %s: %w", dbPath, err)
	}
	defer func() { _ = db.Close() }()

	var rollout sql.NullString
	err = db.QueryRowContext(ctx,
		`SELECT rollout_path FROM threads WHERE id = ?`, id).Scan(&rollout)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("codex thread %s not in %s", id, dbPath)
	}
	if err != nil {
		return "", fmt.Errorf("select rollout_path: %w", err)
	}
	return rollout.String, nil
}

// parseRollout streams the rollout lines into a Session.
func (a *Adapter) parseRollout(path string, m session.Meta) (session.Session, error) {
	st := &loadState{cwd: m.Cwd}
	lines, closeFn, err := openRollout(path)
	if err != nil {
		return session.Session{}, err
	}
	defer closeFn()

	for _, line := range lines {
		st.consume(line, a.log)
	}

	out := session.Session{
		Meta:         m,
		Model:        st.model,
		HarnessVer:   st.cliVer,
		RecordedGit:  st.git,
		Turns:        st.turns,
		Todos:        st.todos,
		FilesTouched: st.files,
		Skipped:      st.skipped,
	}
	if st.cwd != "" {
		out.Cwd = st.cwd
	}
	a.rememberInfo(out.ID, session.Info{MidTurn: st.midTurn})
	return out, nil
}

// consume parses one rollout line and updates the state.
func (s *loadState) consume(line []byte, log func(string, ...any)) {
	var env envelope
	if err := json.Unmarshal(line, &env); err != nil {
		s.skipped++
		return
	}
	s.now = parseTimestamp(env.Timestamp)

	switch env.Type {
	case "session_meta":
		s.applyMeta(env.Payload)
	case "turn_context":
		s.applyTurnContext(env.Payload)
	case "response_item":
		s.applyResponseItem(env.Payload, log)
	case "compacted":
		s.applyCompacted(env.Payload)
	case "event_msg":
		s.applyEventMsg(env.Payload, log)
	default:
		s.skipped++
	}
}

func parseTimestamp(ts string) time.Time {
	if ts == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return time.Time{}
	}
	return t
}

func (s *loadState) applyMeta(raw json.RawMessage) {
	var meta metaPayload
	if err := json.Unmarshal(raw, &meta); err != nil {
		s.skipped++
		return
	}
	s.cliVer = meta.CliVersion
	if meta.Cwd != "" {
		s.cwd = meta.Cwd
	}
	s.git = gitRecordFrom(meta)
}

func (s *loadState) applyTurnContext(raw json.RawMessage) {
	var tc turnContextPayload
	if err := json.Unmarshal(raw, &tc); err != nil {
		s.skipped++
		return
	}
	if tc.Model != "" {
		s.model = tc.Model
	}
	if tc.Cwd != "" {
		s.cwd = tc.Cwd
	}
}

func (s *loadState) applyCompacted(raw json.RawMessage) {
	var c compactedPayload
	if err := json.Unmarshal(raw, &c); err != nil {
		s.skipped++
		return
	}
	if c.Message == "" {
		return
	}
	s.append(session.Turn{Kind: session.CompactionSummary, Time: s.now, Text: c.Message})
}

func (s *loadState) append(t session.Turn) {
	s.turns = append(s.turns, t)
}

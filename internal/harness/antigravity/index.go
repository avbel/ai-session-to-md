package antigravity

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/avbel/ai-session-to-md/internal/jsonl"
	"github.com/avbel/ai-session-to-md/internal/sqlitero"
	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

// summariesSQL reads the agy conversation index. The datetime columns are
// concatenated to text so the driver hands over the stored string instead of
// converting the declared DATETIME type (zero values are 0001-01-01).
const summariesSQL = `SELECT conversation_id, title, workspace_uris, ''||last_modified_time, status,
parent_conversation_id, nesting_depth, not_fully_idle, killed, app_data_dir
FROM conversation_summaries`

const summaryTimeLayout = "2006-01-02 15:04:05.999999999-07:00"

// statusIdle is the index status of a conversation that is not running.
const statusIdle = "CASCADE_RUN_STATUS_IDLE"

// summary is one conversation_summaries row.
type summary struct {
	title      string
	workspaces []string
	updated    time.Time
	status     string
	parent     string
	depth      int
	notIdle    bool
	killed     bool
	app        string
}

// busy reports a conversation agy still considers running.
func (s summary) busy() bool {
	return !s.killed && (s.notIdle || (s.status != "" && s.status != statusIdle))
}

func (s summary) isChild() bool {
	return s.parent != "" || s.depth > 0
}

func (a *Adapter) summariesPath() string {
	return filepath.Join(a.root, cliApp, summariesFile)
}

// readSummaries loads the whole index keyed by conversation id. A missing
// index is normal (desktop-only installs, old agy); an unreadable one is
// warned about and treated as missing, because the transcripts still list.
func (a *Adapter) readSummaries(ctx context.Context) map[string]summary {
	return a.loadSummaries(ctx, a.warn)
}

// loadSummaries is readSummaries with the warning sink chosen by the caller:
// Load passes a no-op, because List already warned about the same index.
func (a *Adapter) loadSummaries(ctx context.Context, warn func(string)) map[string]summary {
	path := a.summariesPath()
	if !isFile(path) {
		return nil
	}
	out, err := querySummaries(ctx, path, false)
	if err == nil {
		return out
	}
	// agy writes the index live; immutable=1 reads the last checkpoint when
	// the WAL cannot be opened read-only.
	out, err2 := querySummaries(ctx, path, true)
	if err2 != nil {
		warn(fmt.Sprintf("cannot read the Antigravity index %s (%v): listing from transcripts only, subagent conversations are not filtered out", path, err2))
		return nil
	}
	a.log("read antigravity index %s: %v; read its last checkpoint instead", path, err)
	warn("reading the Antigravity index snapshot: conversations updated since its last checkpoint may show stale titles or times")
	return out
}

func (a *Adapter) summaryFor(ctx context.Context, id string) (summary, bool) {
	s, ok := a.loadSummaries(ctx, func(string) {})[id]
	return s, ok
}

func querySummaries(ctx context.Context, path string, immutable bool) (map[string]summary, error) {
	uri := sqlitero.URI(path) + "?mode=ro&_pragma=busy_timeout(5000)"
	if immutable {
		uri += "&immutable=1"
	}
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	defer func() { _ = db.Close() }()
	rows, err := db.QueryContext(ctx, summariesSQL)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]summary{}
	for rows.Next() {
		var (
			id, title, uris, updated, status, parent, app sql.NullString
			depth                                         sql.NullInt64
			notIdle, killed                               sql.NullBool
		)
		if err := rows.Scan(&id, &title, &uris, &updated, &status, &parent, &depth, &notIdle, &killed, &app); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		if !idPattern.MatchString(id.String) {
			continue
		}
		out[id.String] = summary{
			title:      strings.TrimSpace(title.String),
			workspaces: workspacePaths(uris.String),
			updated:    parseSummaryTime(updated.String),
			status:     status.String,
			parent:     parent.String,
			depth:      int(depth.Int64),
			notIdle:    notIdle.Bool,
			killed:     killed.Bool,
			app:        app.String,
		}
	}
	return out, rows.Err()
}

func parseSummaryTime(s string) time.Time {
	t, err := time.Parse(summaryTimeLayout, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// workspacePaths decodes the index's JSON list of file:// workspace URIs.
func workspacePaths(raw string) []string {
	var uris []string
	if json.Unmarshal([]byte(raw), &uris) != nil {
		return nil
	}
	var out []string
	for _, u := range uris {
		if p := fileURIPath(u); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// fileURIPath turns file:///Users/x/p (or file:///C:/x) into a local path;
// "" for any other scheme.
func fileURIPath(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "file" || u.Path == "" {
		return ""
	}
	p := u.Path
	if len(p) >= 3 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	return filepath.FromSlash(p)
}

// historyEntry is one line of agy's prompt log, history.jsonl.
type historyEntry struct {
	Workspace      string `json:"workspace"`
	ConversationID string `json:"conversationId"`
}

// readHistory maps conversation ids to the workspace their prompts were
// typed in; the cwd fallback when the index is missing.
func (a *Adapter) readHistory() map[string]string {
	seq, closeFn, err := jsonl.Open(filepath.Join(a.root, cliApp, historyFile))
	if err != nil {
		return nil
	}
	defer closeFn()
	out := map[string]string{}
	for _, raw := range seq {
		var h historyEntry
		if json.Unmarshal(raw, &h) == nil && h.ConversationID != "" && h.Workspace != "" {
			out[h.ConversationID] = h.Workspace
		}
	}
	return out
}

// fallbackCwd finds a workspace without the index: the prompt log, else the
// first command's working directory.
func (a *Adapter) fallbackCwd(id string, recs []record) string {
	if ws := a.readHistory()[id]; ws != "" {
		return ws
	}
	for _, r := range recs {
		if cwd := commandCwd(r); cwd != "" {
			return cwd
		}
	}
	return ""
}

// annotationTitle reads the title agy generated for a conversation,
// annotations/<id>.pbtxt, a one-line text proto: title:"...".
func (a *Adapter) annotationTitle(app, id string) string {
	b, err := os.ReadFile(filepath.Join(a.root, app, "annotations", id+".pbtxt")) //nolint:gosec // G304: id is validated against idPattern
	if err != nil {
		return ""
	}
	for line := range strings.SplitSeq(string(b), "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "title:")
		if !ok {
			continue
		}
		// Text-proto strings use C escapes (octal UTF-8 bytes included),
		// which strconv.Unquote decodes.
		if t, err := strconv.Unquote(strings.TrimSpace(rest)); err == nil {
			return strings.TrimSpace(t)
		}
	}
	return ""
}

package opencode

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/avbel/ai-session-to-md/internal/session"
)

const (
	exportTimeout  = 60 * time.Second
	versionTimeout = 5 * time.Second
)

// errExportShape marks an export document that does not match the expected
// {info, messages} shape.
var errExportShape = errors.New("export shape not recognised")

type exportInfo struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Directory string `json:"directory"`
	// Model may be a JSON string or a {"id","providerID"} object, depending
	// on the export version; a typed string field fails the whole document.
	Model        json.RawMessage `json:"model"`
	Version      string          `json:"version"`
	TimeUpdated  json.RawMessage `json:"time_updated"`
	TimeArchived json.RawMessage `json:"time_archived"`
}

// exportMsg is one export message: the type plus the same fields as a
// session_message data payload, inlined at the top level.
type exportMsg struct {
	Type string `json:"type"`
	msgData
}

type exportDoc struct {
	Info     exportInfo  `json:"info"`
	Messages []exportMsg `json:"messages"`
}

func (a *Adapter) loadExport(ctx context.Context, m session.Meta) (LoadResult, error) {
	if !validSessionID(m.ID) {
		return LoadResult{}, fmt.Errorf("invalid opencode session id %q", m.ID)
	}
	out, err := a.runExport(ctx, m.ID)
	if err != nil {
		return LoadResult{}, err
	}
	return a.buildExport(ctx, m, out)
}

// buildExport parses an export document and folds its messages into a
// normalized session through the same builder as the v2 path.
func (a *Adapter) buildExport(ctx context.Context, m session.Meta, out []byte) (LoadResult, error) {
	doc, perr := parseExport(out)
	if perr != nil {
		return LoadResult{}, fmt.Errorf("unsupported OpenCode version %s", a.opencodeVersion(ctx))
	}
	b := newBuilder(exportMeta(m, doc.Info))
	b.model = modelJSON(string(doc.Info.Model))
	b.ver = doc.Info.Version
	for _, em := range doc.Messages {
		b.addMsg(msg{Type: em.Type, Data: em.msgData})
	}
	return LoadResult{Session: b.build(nil), Info: b.info}, nil
}

func exportMeta(m session.Meta, info exportInfo) session.Meta {
	return session.Meta{
		Harness:   session.HarnessOpenCode,
		ID:        m.ID,
		Source:    "opencode session export #" + m.ID,
		Title:     cmp.Or(info.Title, m.Title),
		Cwd:       cmp.Or(info.Directory, m.Cwd),
		UpdatedAt: msTime(msField(info.TimeUpdated)),
		Archived:  !isNullJSON(info.TimeArchived),
	}
}

// msField reads a time field that may be a number or, in other export
// versions, an object; anything unparseable counts as absent.
func msField(raw json.RawMessage) int64 {
	if isNullJSON(raw) {
		return 0
	}
	var ms int64
	if json.Unmarshal(raw, &ms) == nil {
		return ms
	}
	return 0
}

func isNullJSON(raw json.RawMessage) bool {
	return len(raw) == 0 || string(raw) == "null"
}

// runExport execs `opencode session export <id>` with no shell; the id is a
// separate argv element and was validated by the caller and by validSessionID.
// The exec runs from the user's home, never the session's (untrusted) cwd: a
// repo-local config or hook must not get a chance to run.
func (a *Adapter) runExport(ctx context.Context, id string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, exportTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, a.bin, "session", "export", id)
	if home, err := os.UserHomeDir(); err == nil {
		cmd.Dir = home
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("opencode session export %s: %w: %s", id, err, truncate(stderr.String(), 500))
	}
	return stdout.Bytes(), nil
}

func (a *Adapter) opencodeVersion(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, versionTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, a.bin, "--version").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

// parseExport accepts {info:{…}, messages:[…]}; anything else is an
// errExportShape, which the caller reports as an unsupported version.
func parseExport(data []byte) (exportDoc, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return exportDoc{}, fmt.Errorf("parse export: %w", err)
	}
	if _, ok := probe["messages"]; !ok {
		return exportDoc{}, errExportShape
	}
	var doc exportDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return exportDoc{}, fmt.Errorf("parse export: %w", err)
	}
	return doc, nil
}

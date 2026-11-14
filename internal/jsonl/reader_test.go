package jsonl

import (
	"iter"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func collect(t *testing.T, seq iter.Seq2[int, []byte]) []indexedLine {
	t.Helper()
	var lines []indexedLine
	for i, line := range seq {
		lines = append(lines, indexedLine{i: i, line: string(line)})
	}
	return lines
}

type indexedLine struct {
	i    int
	line string
}

func TestOpenPlain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	content := "{\"n\":1}\n\n{\"n\":2}\n{\"n\":3}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	seq, closeFn, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFn()
	got := collect(t, seq)
	want := []indexedLine{{0, "{\"n\":1}"}, {2, "{\"n\":2}"}, {3, "{\"n\":3}"}}
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("line %d: got (%d,%q), want (%d,%q)", i, got[i].i, got[i].line, want[i].i, want[i].line)
		}
	}
}

func TestOpenNoTrailingNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(path, []byte("{\"a\":1}\n{\"b\":2}"), 0o644); err != nil {
		t.Fatal(err)
	}
	seq, closeFn, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFn()
	got := collect(t, seq)
	if len(got) != 2 || got[1].line != "{\"b\":2}" || got[1].i != 1 {
		t.Fatalf("truncated last line lost: %v", got)
	}
}

func TestOpenCRLF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(path, []byte("\"a\"\r\n\"b\"\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seq, closeFn, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFn()
	got := collect(t, seq)
	if len(got) != 2 || got[0].line != "\"a\"" || got[1].line != "\"b\"" {
		t.Fatalf("CRLF not trimmed: %v", got)
	}
}

func TestOpenZstd(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl.zst")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw, err := zstd.NewWriter(f)
	if err != nil {
		t.Fatal(err)
	}
	content := "{\"first\":true}\n\n{\"second\":\"" + strings.Repeat("z", 300) + "\"}\n"
	if _, err := zw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	seq, closeFn, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFn()
	got := collect(t, seq)
	if len(got) != 2 {
		t.Fatalf("got %d lines, want 2: %v", len(got), got)
	}
	if got[0].line != "{\"first\":true}" || got[0].i != 0 {
		t.Fatalf("line 0: %v", got[0])
	}
	if !strings.HasPrefix(got[1].line, "{\"second\":\"") || got[1].i != 2 {
		t.Fatalf("line 1: %v", got[1])
	}
}

func TestOpenZstdUppercaseSuffix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.JSONL.ZST")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw, err := zstd.NewWriter(f)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := zw.Write([]byte("{\"k\":1}\n")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	seq, closeFn, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFn()
	got := collect(t, seq)
	if len(got) != 1 || got[0].line != "{\"k\":1}" {
		t.Fatalf("got %v", got)
	}
}

func TestOpenTwoMegabyteLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "huge.jsonl")
	big := "{\"blob\":\"" + strings.Repeat("a", 2<<20) + "\"}"
	if err := os.WriteFile(path, []byte("{\"small\":1}\n"+big+"\n{\"small\":2}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seq, closeFn, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFn()
	var sizes []int
	for _, line := range seq {
		sizes = append(sizes, len(line))
	}
	if len(sizes) != 3 {
		t.Fatalf("got %d lines, want 3", len(sizes))
	}
	if sizes[1] != len(big) {
		t.Fatalf("long line truncated: got %d bytes, want %d", sizes[1], len(big))
	}
}

func TestOpenAllEmptyLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.jsonl")
	if err := os.WriteFile(path, []byte("\n\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seq, closeFn, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFn()
	if got := collect(t, seq); len(got) != 0 {
		t.Fatalf("empty lines not skipped: %v", got)
	}
}

func TestOpenMissingFile(t *testing.T) {
	_, _, err := Open(filepath.Join(t.TempDir(), "nope.jsonl"))
	if err == nil {
		t.Fatal("want error for missing file")
	}
}

func TestReaderFromIOReader(t *testing.T) {
	seq := Reader(strings.NewReader("one\ntwo\n"))
	got := collect(t, seq)
	if len(got) != 2 || got[0].line != "one" || got[1].line != "two" {
		t.Fatalf("got %v", got)
	}
}

func TestReaderEarlyBreak(t *testing.T) {
	seq := Reader(strings.NewReader("one\ntwo\nthree\n"))
	count := 0
	for range seq {
		count++
		if count == 2 {
			break
		}
	}
	if count != 2 {
		t.Fatalf("count %d, want 2", count)
	}
}

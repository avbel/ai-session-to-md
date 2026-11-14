package jsonl

import (
	"bufio"
	"bytes"
	"io"
	"iter"
	"os"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// Open returns an iterator over the non-empty lines of the file at path,
// numbered by their physical line index, plus a cleanup func that must be
// called even when iteration is abandoned early. A .zst suffix (case
// insensitive) selects transparent zstd decompression.
//
// Read errors other than EOF end the iteration silently; callers detect lost
// tail data by failing to parse what they get, never by a fatal error.
func Open(path string) (iter.Seq2[int, []byte], func(), error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	closeFn := func() {
		_ = f.Close()
	}
	var r io.Reader = f
	if strings.HasSuffix(strings.ToLower(path), ".zst") {
		zr, err := zstd.NewReader(f)
		if err != nil {
			_ = f.Close()
			return nil, nil, err
		}
		r = zr
		closeFn = func() {
			zr.Close()
			_ = f.Close()
		}
	}
	return fromReader(r), closeFn, nil
}

// Reader iterates the non-empty lines of r, numbered by their physical line
// index. The caller keeps ownership of r and closes it if needed.
func Reader(r io.Reader) iter.Seq2[int, []byte] {
	return fromReader(r)
}

func fromReader(r io.Reader) iter.Seq2[int, []byte] {
	return func(yield func(int, []byte) bool) {
		br := bufio.NewReaderSize(r, 1<<20)
		for i := 0; ; i++ {
			chunk, err := br.ReadBytes('\n')
			if len(chunk) > 0 {
				line := trimEOL(chunk)
				if len(line) > 0 && !yield(i, line) {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}
}

func trimEOL(b []byte) []byte {
	b = bytes.TrimSuffix(b, []byte("\n"))
	return bytes.TrimSuffix(b, []byte("\r"))
}

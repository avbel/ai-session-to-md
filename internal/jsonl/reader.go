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
// insensitive) selects transparent zstd decompression. A line longer than
// 64 MiB is yielded truncated with a marker, and iteration continues.
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

// maxLineBytes caps one line's length (§2): session lines can be several
// MB, and an unbounded ReadBytes over /dev/zero-style input would OOM. A
// var only so tests can shrink it.
var maxLineBytes = int64(64 << 20)

// overLongMarker makes a too-long line unparseable and self-describing;
// callers skip and count it as a malformed line (§2 tolerance).
const overLongMarker = "…[line truncated, over the 64 MiB cap]…"

// Reader iterates the non-empty lines of r, numbered by their physical line
// index. The caller keeps ownership of r and closes it if needed. A line
// longer than 64 MiB is yielded truncated, with a marker appended, and
// reading continues after it.
func Reader(r io.Reader) iter.Seq2[int, []byte] {
	return fromReader(r)
}

func fromReader(r io.Reader) iter.Seq2[int, []byte] {
	return func(yield func(int, []byte) bool) {
		br := bufio.NewReaderSize(r, 1<<20)
		for i := 0; ; i++ {
			line, truncated, err := readLine(br)
			out := line
			if truncated {
				out = append(line, overLongMarker...)
			} else {
				out = trimEOL(line)
			}
			if len(out) > 0 && !yield(i, out) {
				return
			}
			if err != nil {
				return
			}
		}
	}
}

// readLine accumulates one line, stopping early once maxLineBytes is
// reached: the rest of the line is discarded until the next newline. The
// error from the final read (EOF, or a read failure) is passed through so
// the caller can stop; the yielded data is what was read so far.
func readLine(br *bufio.Reader) ([]byte, bool, error) {
	var line []byte
	for {
		chunk, err := br.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			// Buffer full, no newline yet: keep what still fits.
			if int64(len(line)) < maxLineBytes {
				room := maxLineBytes - int64(len(line))
				if int64(len(chunk)) > room {
					line = append(line, chunk[:room]...)
					continue
				}
				line = append(line, chunk...)
			}
			continue
		}
		// Final chunk: strip the line terminator, then apply the cap so a
		// line exactly at the cap is not reported as over-long.
		content := chunk
		if err == nil {
			content = trimEOL(chunk)
		}
		if room := maxLineBytes - int64(len(line)); int64(len(content)) > room {
			return append(line, content[:max(room, 0)]...), true, err
		}
		return append(line, content...), false, err
	}
}

func trimEOL(b []byte) []byte {
	b = bytes.TrimSuffix(b, []byte("\n"))
	return bytes.TrimSuffix(b, []byte("\r"))
}

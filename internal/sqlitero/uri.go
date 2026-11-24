// Package sqlitero builds the file: URIs used to open harness SQLite stores
// read-only through the modernc.org/sqlite driver.
package sqlitero

import (
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

// winDrive matches Windows drive-letter paths like C:/…, which are not
// filepath.IsAbs on non-Windows hosts.
var winDrive = regexp.MustCompile(`^[A-Za-z]:/`)

// URI builds the file: URI consumed by the SQLite driver. Absolute paths
// always become file:///… (a Windows drive as file:///C:/…) with forward
// slashes, on every OS; relative paths become file:relative/… . The form
// follows the path shape, not runtime.GOOS, so it behaves identically
// everywhere and is testable on any platform.
func URI(path string) string {
	// ToSlash alone is not enough: on unix a backslash is an ordinary
	// filename byte, so Windows drive paths must be converted explicitly.
	slash := strings.ReplaceAll(filepath.ToSlash(path), `\`, "/")
	// "/"-prefixed paths are absolute on unix but not on Windows, so the
	// shape check decides, not filepath.IsAbs alone.
	abs := filepath.IsAbs(path) || winDrive.MatchString(slash) || strings.HasPrefix(slash, "/")
	if !abs {
		return "file:" + escapeSegments(slash)
	}
	if !strings.HasPrefix(slash, "/") {
		slash = "/" + slash
	}
	return "file://" + escapeSegments(slash)
}

// escapeSegments percent-escapes every path segment; the SQLite driver
// decodes the URI path itself, so # or ? in a directory name cannot truncate
// the URI or turn the mode=ro query parameter into part of the filename.
func escapeSegments(p string) string {
	seg := strings.Split(p, "/")
	for i, s := range seg {
		seg[i] = url.PathEscape(s)
	}
	return strings.Join(seg, "/")
}

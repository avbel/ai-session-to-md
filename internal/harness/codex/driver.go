package codex

// The pure-Go SQLite driver registers itself for database/sql; every
// database/sql open in this package relies on it.
import _ "modernc.org/sqlite"

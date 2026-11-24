package sqlitero

import "testing"

func TestURI(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/tmp/proj/opencode.db", "file:///tmp/proj/opencode.db"},
		{`C:\Users\avbel\AppData\opencode.db`, "file:///C:/Users/avbel/AppData/opencode.db"},
		{`C:/Users/x/opencode.db`, "file:///C:/Users/x/opencode.db"},
		{"relative/opencode.db", "file:relative/opencode.db"},
		{"/tmp/store#x/opencode.db", "file:///tmp/store%23x/opencode.db"},
		{"/tmp/store?x/opencode.db", "file:///tmp/store%3Fx/opencode.db"},
		{"/tmp/sp ace/opencode.db", "file:///tmp/sp%20ace/opencode.db"},
	}
	for _, tc := range cases {
		if got := URI(tc.in); got != tc.want {
			t.Errorf("URI(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

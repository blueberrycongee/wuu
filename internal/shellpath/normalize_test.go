package shellpath

import "testing"

func TestRewriteCmdNullRedirect(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"go test ./... 2>nul", "go test ./... 2>/dev/null"},
		{"go test ./... 2> nul", "go test ./... 2> /dev/null"},
		{"echo hi >nul", "echo hi >/dev/null"},
		{"echo hi >NUL", "echo hi >/dev/null"},
		{"cmd 2>>nul", "cmd 2>>/dev/null"},
		{"cmd 2>nul; echo done", "cmd 2>/dev/null; echo done"},
		{"cmd &>nul", "cmd &>/dev/null"},
		// Real filenames that merely start with nul stay untouched.
		{"cat 2>nul.txt", "cat 2>nul.txt"},
		{"grep nul file", "grep nul file"},
		{"echo 2>/dev/null", "echo 2>/dev/null"},
		// Quoted text is data, not redirection.
		{`echo "uses 2>nul on cmd"`, `echo "uses 2>nul on cmd"`},
		{`echo 'literal 2>nul'`, `echo 'literal 2>nul'`},
		{`grep "2>nul" doc.md 2>nul`, `grep "2>nul" doc.md 2>/dev/null`},
		{`echo \" 2>nul`, `echo \" 2>/dev/null`},
		// Unbalanced quotes: leave the ambiguous tail alone.
		{`echo "unterminated 2>nul`, `echo "unterminated 2>nul`},
		// Heredocs carry arbitrary body text; skip the command entirely.
		{"cat <<EOF\n2>nul\nEOF", "cat <<EOF\n2>nul\nEOF"},
	}
	for _, tc := range cases {
		if got := rewriteCmdNullRedirect(tc.in); got != tc.want {
			t.Errorf("rewriteCmdNullRedirect(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

package web

import (
	"bytes"
	"strings"
	"testing"
)

func TestAssetURL(t *testing.T) {
	if got := assetURL("abc1234", "/static/style.css"); got != "/static/style.css?v=abc1234" {
		t.Fatalf("got %q", got)
	}
}

// The asset template func must stamp static URLs with the injected build
// revision, and with a non-empty fallback (process start time) when none is
// injected.
func TestAssetFuncStampsVersion(t *testing.T) {
	for _, tc := range []struct{ version, want string }{
		{"abc1234", "/static/style.css?v=abc1234"},
		{"", "/static/style.css?v="}, // fallback must still carry ?v=<something>
	} {
		s := New(nil, "u", "p", nil, tc.version)
		var buf bytes.Buffer
		if err := s.tpl.ExecuteTemplate(&buf, "head", map[string]any{"T": catalogs["en"]}); err != nil {
			t.Fatalf("render head (version %q): %v", tc.version, err)
		}
		out := buf.String()
		if !strings.Contains(out, tc.want) {
			t.Fatalf("render head (version %q): %q not in output", tc.version, tc.want)
		}
		// The fallback must be a non-empty stamp, not a bare "?v=".
		if strings.Contains(out, `/static/style.css?v="`) {
			t.Fatalf("render head (version %q): empty version stamp", tc.version)
		}
	}
}

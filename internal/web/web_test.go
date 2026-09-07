package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAvatarInitial(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Alice <alice@example.com>", "A"},
		{"alice@example.com", "A"},
		{"张三 <zhang@example.cn>", "张"},
		{"", "?"},
		{"  ", "?"},
	} {
		if got := avatarInitial(tc.in); got != tc.want {
			t.Errorf("avatarInitial(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestAvatarHue(t *testing.T) {
	// Same sender (whether bare or with a display name) gets the same hue.
	if avatarHue("Alice <alice@example.com>") != avatarHue("alice@example.com") {
		t.Fatal("hue must depend on the address, not the display name")
	}
	for _, from := range []string{"a@example.com", "b@example.com", "张三 <z@example.cn>"} {
		h := avatarHue(from)
		if h < 0 || h >= 360 {
			t.Fatalf("avatarHue(%q) = %d, out of range", from, h)
		}
	}
}

func TestHumanSize(t *testing.T) {
	for _, tc := range []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{25 << 20, "25.0 MB"},
	} {
		if got := humanSize(tc.in); got != tc.want {
			t.Errorf("humanSize(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFormatFullDate(t *testing.T) {
	got := formatFullDate("Tue, 01 Jul 2025 10:00:00 +0000")
	if !strings.Contains(got, "2025-07-01") {
		t.Fatalf("formatFullDate = %q", got)
	}
	// Unparseable values pass through untouched.
	if got := formatFullDate("garbage"); got != "garbage" {
		t.Fatalf("formatFullDate(garbage) = %q", got)
	}
}

// Unauthenticated API requests must get a 401, not a login redirect that a
// fetch caller cannot usefully follow.
func TestRequireAuthAPIReturns401(t *testing.T) {
	s := testServer()
	h := s.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	r := httptest.NewRequest("POST", "/api/push/subscribe", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("api: got %d, want 401", w.Code)
	}

	r = httptest.NewRequest("GET", "/", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("page: got %d, want 303", w.Code)
	}
}

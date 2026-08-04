package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNegotiateLang(t *testing.T) {
	for _, tc := range []struct {
		header string
		want   string
	}{
		{"zh-CN,zh;q=0.9", "zh"},
		{"en-US,en;q=0.9", "en"},
		// Higher q wins even when zh appears first.
		{"zh;q=0.6,en-US;q=0.9", "en"},
		{"en-US,en;q=0.9,zh-CN;q=0.8", "en"},
		{"fr-FR,fr;q=0.9", ""},
		{"", ""},
	} {
		if got := negotiateLang(tc.header); got != tc.want {
			t.Errorf("negotiateLang(%q) = %q, want %q", tc.header, got, tc.want)
		}
	}
}

func TestCatalogFor(t *testing.T) {
	// Cookie wins over Accept-Language.
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Accept-Language", "zh-CN")
	r.AddCookie(&http.Cookie{Name: langCookieName, Value: "en"})
	if got := catalogFor(r).Lang; got != "en" {
		t.Fatalf("cookie override: got %q, want en", got)
	}

	// Accept-Language is used without a cookie.
	r = httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Accept-Language", "en-US,en;q=0.9")
	if got := catalogFor(r).Lang; got != "en" {
		t.Fatalf("accept-language: got %q, want en", got)
	}

	// Neither falls back to the default; a garbage cookie is ignored.
	r = httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: langCookieName, Value: "klingon"})
	if got := catalogFor(r).Lang; got != defaultLang {
		t.Fatalf("default: got %q, want %q", got, defaultLang)
	}
}

package web

import (
	"strings"
	"testing"
)

func TestWithBaseTargetBlank(t *testing.T) {
	const base = `<base target="_blank">`
	cases := []struct {
		name string
		in   string
		want string // substring that must appear, in order position check below
	}{
		{"with head tag", `<html><head><title>t</title></head><body>x</body></html>`, `<head>` + base},
		{"with head attrs", `<HEAD lang="en"><title>t</title></HEAD>`, `<HEAD lang="en">` + base},
		{"no head", `<p>hello</p>`, base + `<p>hello</p>`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := withBaseTargetBlank(c.in)
			if !strings.Contains(got, c.want) {
				t.Fatalf("got %q, want it to contain %q", got, c.want)
			}
		})
	}
}

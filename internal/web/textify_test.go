package web

import "testing"

func TestHTMLToText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			"paragraphs and inline markup",
			`<html><head><title>t</title></head><body><p>Hello <b>world</b></p><p>Second&nbsp;line</p></body></html>`,
			"Hello world\nSecond line",
		},
		{
			"br and div",
			`<div>one<br>two</div><div>three</div>`,
			"one\ntwo\nthree",
		},
		{
			"script and style dropped",
			`<style>.a{color:red}</style><p>hi</p><script>alert(1)</script>`,
			"hi",
		},
		{
			"list items",
			`<ul><li>a</li><li>b</li></ul>`,
			"- a\n- b",
		},
		{
			"whitespace collapsed",
			"<p>  lots   of\n\n   space  </p>\n\n\n<p>next</p>",
			"lots of space\nnext",
		},
		{
			"table cells separated",
			`<table><tr><td>a</td><td>b</td></tr></table>`,
			"a b",
		},
		{
			"entities decoded",
			`<p>1 &lt; 2 &amp; 3 &gt; 2</p>`,
			"1 < 2 & 3 > 2",
		},
		{
			"empty input",
			``,
			"",
		},
		{
			"only scripts",
			`<html><body><script>x()</script></body></html>`,
			"",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := htmlToText(c.in); got != c.want {
				t.Fatalf("htmlToText(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

package web

import "testing"

// Regression: go-message already converts charset to UTF-8 on Read; the
// body must not be converted a second time.
func TestWalkMessageGBKBody(t *testing.T) {
	raw := "Content-Type: text/plain; charset=\"gb2312\"\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Transfer-Encoding: base64\r\n" +
		"From: a@b.c\r\n\r\n" +
		"1eLKx9K7t+IgR0IyMzEyILHgwuu1xNX9zsQ=\r\n" // "这是一封 GB2312 编码的正文" in GBK
	text, _, _ := walkMessage([]byte(raw))
	want := "这是一封 GB2312 编码的正文"
	if text != want+"\n" && text != want+"\r\n" && text != want {
		t.Fatalf("text = %q, want %q", text, want)
	}
}

// Package web implements the read-only mail viewing UI.
package web

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"mime"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-message"
	// Registers charset decoders (GB2312/GBK/Big5/...) into go-message's
	// global registry, so message.Read converts bodies to UTF-8 itself.
	_ "github.com/emersion/go-message/charset"
	"github.com/yankeguo/airmx/internal/maildir"
	"github.com/yankeguo/airmx/internal/push"
	htmlcharset "golang.org/x/net/html/charset"
)

//go:embed templates
var templatesFS embed.FS

//go:embed static
var staticFS embed.FS

type Server struct {
	store        *maildir.Store
	username     string
	passwordHash []byte
	push         *push.Service // nil disables Web Push
	swJS         []byte
	tpl          *template.Template
	mux          *http.ServeMux
}

func New(store *maildir.Store, username, passwordBcrypt string, pushSvc *push.Service, assetVersion string) *Server {
	// Without an injected build revision, fall back to the process start
	// time so cache busting still works on restart.
	if assetVersion == "" {
		assetVersion = strconv.FormatInt(time.Now().Unix(), 36)
	}
	s := &Server{
		store:        store,
		username:     username,
		passwordHash: []byte(passwordBcrypt),
		push:         pushSvc,
		swJS:         must(staticFS.ReadFile("static/sw.js")),
		tpl: template.Must(template.New("").Funcs(template.FuncMap{
			"fdate": formatListDate,
			"add":   func(a, b int) int { return a + b },
			"sub":   func(a, b int) int { return a - b },
			"asset": func(p string) string { return assetURL(assetVersion, p) },
		}).ParseFS(templatesFS, "templates/*.html")),
	}
	mux := http.NewServeMux()
	// Public routes. Static assets must be reachable from the login page,
	// so they live outside the auth middleware; the service worker must sit
	// at the root to control the whole origin.
	mux.Handle("GET /static/", withCache(http.FileServerFS(staticFS)))
	mux.HandleFunc("GET /sw.js", s.handleServiceWorker)
	mux.HandleFunc("GET /login", s.handleLogin)
	mux.HandleFunc("POST /login", s.handleLogin)
	mux.HandleFunc("GET /logout", s.handleLogout)
	mux.HandleFunc("GET /lang", s.handleLang)
	// Protected routes.
	mux.Handle("GET /{$}", s.requireAuth(http.HandlerFunc(s.handleList)))
	mux.Handle("GET /mail/{id}", s.requireAuth(http.HandlerFunc(s.handleView)))
	mux.Handle("GET /mail/{id}/html", s.requireAuth(http.HandlerFunc(s.handleHTML)))
	mux.Handle("GET /mail/{id}/attach/{n}", s.requireAuth(http.HandlerFunc(s.handleAttach)))
	mux.Handle("POST /mail/{id}/delete", s.requireAuth(http.HandlerFunc(s.handleDelete)))
	mux.Handle("POST /api/push/subscribe", s.requireAuth(http.HandlerFunc(s.handlePushSubscribe)))
	mux.Handle("POST /api/push/unsubscribe", s.requireAuth(http.HandlerFunc(s.handlePushUnsubscribe)))
	s.mux = mux
	return s
}

// ListenAndServe serves the UI. All pages except /login require a valid
// session cookie; see auth.go.
func (s *Server) ListenAndServe(addr string) error {
	log.Printf("web: listening on %s", addr)
	return http.ListenAndServe(addr, s.mux)
}

// withCache sets a long cache lifetime on embedded static assets: their
// URLs carry a build-revision query (?v=, see the asset template func), so
// the content at any given URL never changes.
func withCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=604800")
		next.ServeHTTP(w, r)
	})
}

// assetURL appends the build revision to a static asset path for cache
// busting: /static/style.css -> /static/style.css?v=abc1234.
func assetURL(version, path string) string {
	return path + "?v=" + version
}

type listData struct {
	T        catalog
	Messages []maildir.Message
	Page     int
	Pages    int
	Total    int
	// PushKey is the VAPID public key, empty when Web Push is disabled.
	PushKey string
}

// listPageSize is the number of messages shown per page.
const listPageSize = 50

// handleList renders the unified message listing, paginated with ?p=N.
func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	msgs, err := s.store.List()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	total := len(msgs)
	pages := (total + listPageSize - 1) / listPageSize
	if pages == 0 {
		pages = 1
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("p"))
	if page < 1 {
		page = 1
	}
	if page > pages {
		page = pages
	}
	start := (page - 1) * listPageSize
	end := min(start+listPageSize, total)
	var pushKey string
	if s.push != nil {
		pushKey = s.push.PublicKey()
	}
	s.render(w, "list.html", listData{
		T:        catalogFor(r),
		Messages: msgs[start:end],
		Page:     page,
		Pages:    pages,
		Total:    total,
		PushKey:  pushKey,
	})
}

type attachment struct {
	N        int
	Filename string
	Type     string
	Size     string
}

type viewData struct {
	T        catalog
	ID       string
	Subject  string
	From     string
	To       string
	Date     string
	Spam     bool
	TextBody string
	HasHTML  bool
	Attach   []attachment
}

// formatListDate renders a compact timestamp for the message list: time only
// for mail from today, month-day within the year, full date for older mail.
func formatListDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	now := time.Now()
	y, m, d := t.Local().Date()
	ny, nm, nd := now.Date()
	switch {
	case y == ny && m == nm && d == nd:
		return t.Local().Format("15:04")
	case y == ny:
		return t.Local().Format("01-02")
	default:
		return t.Local().Format("2006-01-02")
	}
}

// walkMessage classifies the entities of a message: plain text body, HTML
// body (both already decoded to UTF-8 by go-message), and attachments (by
// walk order).
func walkMessage(raw []byte) (text, htmlBody string, attach []attachment) {
	// Read may return an error for unknown charsets/encodings while still
	// returning a usable entity; walk whatever we got.
	e, _ := message.Read(strings.NewReader(string(raw)))
	if e == nil {
		return "", "", nil
	}
	n := 0
	_ = e.Walk(func(_ []int, part *message.Entity, err error) error {
		if err != nil || part == nil {
			return nil
		}
		mt, params, _ := part.Header.ContentType()
		disp, dparams, _ := part.Header.ContentDisposition()
		filename := dparams["filename"]
		if filename == "" {
			filename = params["name"]
		}
		isAttach := disp == "attachment" || filename != ""
		switch {
		case !isAttach && mt == "text/plain" && text == "":
			text = readBody(part)
		case !isAttach && mt == "text/html" && htmlBody == "":
			htmlBody = readBody(part)
		case isAttach:
			if filename == "" {
				filename = fmt.Sprintf("part-%d", n)
			}
			attach = append(attach, attachment{N: n, Filename: filename, Type: mt})
			n++
		}
		return nil
	})
	return text, htmlBody, attach
}

// readBody returns the decoded body of an entity. go-message already
// converts transfer encoding and charset to UTF-8 on Read, so no further
// conversion is needed (doing it again would double-decode).
func readBody(e *message.Entity) string {
	b, err := io.ReadAll(e.Body)
	if err != nil {
		return ""
	}
	return string(b)
}

func (s *Server) handleView(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	raw, err := s.store.Open(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h := headerOf(raw)
	text, htmlBody, attach := walkMessage(raw)
	// Messages without a text/plain part get one derived from the HTML, so
	// the default view never needs to load the HTML (and its remote images).
	if text == "" && htmlBody != "" {
		text = htmlToText(htmlBody)
	}
	s.render(w, "view.html", viewData{
		T:        catalogFor(r),
		ID:       id,
		Subject:  decodeHeader(h.Get("Subject")),
		From:     decodeHeader(h.Get("From")),
		To:       decodeHeader(h.Get("To")),
		Date:     h.Get("Date"),
		Spam:     strings.Contains(h.Get("X-Spam-Status"), "action=spam"),
		TextBody: text,
		HasHTML:  htmlBody != "",
		Attach:   attach,
	})
}

// headerDecoder decodes RFC 2047 encoded-words in headers, including
// non-UTF-8 charsets such as GB2312/Big5 (stdlib alone handles only UTF-8).
var headerDecoder = &mime.WordDecoder{CharsetReader: htmlcharset.NewReaderLabel}

func decodeHeader(s string) string {
	if d, err := headerDecoder.DecodeHeader(s); err == nil {
		return d
	}
	return s
}

func headerOf(raw []byte) mail.Header {
	msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		return mail.Header{}
	}
	return msg.Header
}

// handleHTML serves the decoded HTML body in isolation; the parent page
// embeds it in a sandboxed iframe.
func (s *Server) handleHTML(w http.ResponseWriter, r *http.Request) {
	raw, err := s.store.Open(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_, htmlBody, _ := walkMessage(raw)
	if htmlBody == "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// No scripts, no forms; only images and inline styles may load. base-uri
	// is intentionally not restricted: we inject <base target="_blank"> so
	// links open in a new tab instead of navigating the sandboxed frame
	// (only the first <base> in a document takes effect, so ours wins).
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; img-src data: https:; style-src 'unsafe-inline'; form-action 'none'")
	io.WriteString(w, withBaseTargetBlank(htmlBody))
}

// withBaseTargetBlank injects <base target="_blank"> right after the first
// <head ...> tag, or prepends it when no head tag exists.
func withBaseTargetBlank(html string) string {
	const base = `<base target="_blank">`
	lower := strings.ToLower(html)
	if i := strings.Index(lower, "<head"); i >= 0 {
		if j := strings.Index(lower[i:], ">"); j >= 0 {
			k := i + j + 1
			return html[:k] + base + html[k:]
		}
	}
	return base + html
}

func (s *Server) handleAttach(w http.ResponseWriter, r *http.Request) {
	raw, err := s.store.Open(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var want int
	if _, err := fmt.Sscanf(r.PathValue("n"), "%d", &want); err != nil {
		http.NotFound(w, r)
		return
	}
	e, err := message.Read(strings.NewReader(string(raw)))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	n := 0
	found := false
	_ = e.Walk(func(_ []int, part *message.Entity, err error) error {
		if found || err != nil || part == nil {
			return nil
		}
		disp, dparams, _ := part.Header.ContentDisposition()
		mt, params, _ := part.Header.ContentType()
		filename := dparams["filename"]
		if filename == "" {
			filename = params["name"]
		}
		if disp != "attachment" && filename == "" {
			return nil
		}
		if n == want {
			found = true
			if filename == "" {
				filename = fmt.Sprintf("part-%d", n)
			}
			// Headers must be set inside the walk; body read after.
			w.Header().Set("Content-Type", mt)
			w.Header().Set("Content-Disposition",
				fmt.Sprintf("attachment; filename=%q", filename))
			io.Copy(w, part.Body)
		}
		n++
		return nil
	})
	if !found {
		http.NotFound(w, r)
	}
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Delete(r.PathValue("id")); err != nil {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleServiceWorker serves the push service worker from the root scope.
// It is never cached so updates roll out on the next page load.
func (s *Server) handleServiceWorker(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, "sw.js", time.Time{}, bytes.NewReader(s.swJS))
}

// handlePushSubscribe stores a browser push subscription
// (PushSubscription.toJSON() as the request body).
func (s *Server) handlePushSubscribe(w http.ResponseWriter, r *http.Request) {
	if s.push == nil {
		http.NotFound(w, r)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	if err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if err := s.push.Subscribe(body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handlePushUnsubscribe removes a subscription by endpoint.
func (s *Server) handlePushUnsubscribe(w http.ResponseWriter, r *http.Request) {
	if s.push == nil {
		http.NotFound(w, r)
		return
	}
	var req struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil || req.Endpoint == "" {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if err := s.push.Unsubscribe(req.Endpoint); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("web: render %s: %v", name, err)
	}
}

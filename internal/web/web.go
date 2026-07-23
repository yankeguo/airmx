// Package web implements the read-only mail viewing UI.
package web

import (
	"embed"
	"fmt"
	"html/template"
	"io"
	"log"
	"mime"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/emersion/go-message"
	"github.com/emersion/go-message/charset"
	"github.com/yankeguo/airmx/internal/maildir"
)

//go:embed templates
var templatesFS embed.FS

//go:embed static
var staticFS embed.FS

type Server struct {
	store        *maildir.Store
	username     string
	passwordHash []byte
	tpl          *template.Template
	mux          *http.ServeMux
}

func New(store *maildir.Store, username, passwordBcrypt string) *Server {
	s := &Server{
		store:        store,
		username:     username,
		passwordHash: []byte(passwordBcrypt),
		tpl: template.Must(template.New("").Funcs(template.FuncMap{
			"fdate": formatListDate,
		}).ParseFS(templatesFS, "templates/*.html")),
	}
	mux := http.NewServeMux()
	// Public routes. Static assets must be reachable from the login page,
	// so they live outside the auth middleware.
	mux.Handle("GET /static/", withCache(http.FileServerFS(staticFS)))
	mux.HandleFunc("GET /login", s.handleLogin)
	mux.HandleFunc("POST /login", s.handleLogin)
	mux.HandleFunc("GET /logout", s.handleLogout)
	// Protected routes.
	mux.Handle("GET /{$}", s.requireAuth(http.HandlerFunc(s.handleIndex)))
	mux.Handle("GET /mail/{folder}", s.requireAuth(http.HandlerFunc(s.handleList)))
	mux.Handle("GET /mail/{folder}/{id}", s.requireAuth(http.HandlerFunc(s.handleView)))
	mux.Handle("GET /mail/{folder}/{id}/html", s.requireAuth(http.HandlerFunc(s.handleHTML)))
	mux.Handle("GET /mail/{folder}/{id}/attach/{n}", s.requireAuth(http.HandlerFunc(s.handleAttach)))
	mux.Handle("POST /mail/{folder}/{id}/delete", s.requireAuth(http.HandlerFunc(s.handleDelete)))
	s.mux = mux
	return s
}

// ListenAndServe serves the UI. All pages except /login require a valid
// session cookie; see auth.go.
func (s *Server) ListenAndServe(addr string) error {
	log.Printf("web: listening on %s", addr)
	return http.ListenAndServe(addr, s.mux)
}

// withCache sets a short cache lifetime on embedded static assets; they are
// content-stable between releases and re-fetched on restart anyway.
func withCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		next.ServeHTTP(w, r)
	})
}

// parseFolder validates the folder path parameter.
func parseFolder(w http.ResponseWriter, r *http.Request) (maildir.Folder, bool) {
	f := maildir.Folder(r.PathValue("folder"))
	if !f.Valid() {
		http.NotFound(w, r)
		return "", false
	}
	return f, true
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/mail/inbox", http.StatusFound)
}

type listData struct {
	Folder   string
	Messages []maildir.Message
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	folder, ok := parseFolder(w, r)
	if !ok {
		return
	}
	msgs, err := s.store.List(folder)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, "list.html", listData{Folder: string(folder), Messages: msgs})
}

type attachment struct {
	N        int
	Filename string
	Type     string
	Size     string
}

type viewData struct {
	Folder   string
	ID       string
	Subject  string
	From     string
	To       string
	Date     string
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
// body (both decoded to UTF-8), and attachments (by walk order).
func walkMessage(raw []byte) (text, htmlBody string, attach []attachment) {
	e, err := message.Read(strings.NewReader(string(raw)))
	if err != nil {
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
			text = readTextBody(part, params["charset"])
		case !isAttach && mt == "text/html" && htmlBody == "":
			htmlBody = readTextBody(part, params["charset"])
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

func readTextBody(e *message.Entity, cs string) string {
	var r io.Reader = e.Body
	if cs != "" {
		if cr, err := charset.Reader(cs, e.Body); err == nil {
			r = cr
		}
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return ""
	}
	return string(b)
}

func (s *Server) handleView(w http.ResponseWriter, r *http.Request) {
	folder, ok := parseFolder(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	raw, err := s.store.Open(folder, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h := headerOf(raw)
	text, htmlBody, attach := walkMessage(raw)
	s.render(w, "view.html", viewData{
		Folder:   string(folder),
		ID:       id,
		Subject:  decodeHeader(h.Get("Subject")),
		From:     decodeHeader(h.Get("From")),
		To:       decodeHeader(h.Get("To")),
		Date:     h.Get("Date"),
		TextBody: text,
		HasHTML:  htmlBody != "",
		Attach:   attach,
	})
}

func decodeHeader(s string) string {
	if d, err := new(mime.WordDecoder).DecodeHeader(s); err == nil {
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
	folder, ok := parseFolder(w, r)
	if !ok {
		return
	}
	raw, err := s.store.Open(folder, r.PathValue("id"))
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
	// No scripts, no forms, no base-tag rewriting, no external requests
	// other than images and inline styles.
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; img-src data: https:; style-src 'unsafe-inline'; form-action 'none'; base-uri 'none'")
	io.WriteString(w, htmlBody)
}

func (s *Server) handleAttach(w http.ResponseWriter, r *http.Request) {
	folder, ok := parseFolder(w, r)
	if !ok {
		return
	}
	raw, err := s.store.Open(folder, r.PathValue("id"))
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
	folder, ok := parseFolder(w, r)
	if !ok {
		return
	}
	if err := s.store.Delete(folder, r.PathValue("id")); err != nil {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/mail/"+string(folder), http.StatusSeeOther)
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("web: render %s: %v", name, err)
	}
}

// Package web implements the read-only mail viewing UI.
package web

import (
	"crypto/subtle"
	"embed"
	"fmt"
	"html/template"
	"io"
	"log"
	"mime"
	"net/http"
	"net/mail"
	"strings"

	"github.com/emersion/go-message"
	"github.com/emersion/go-message/charset"
	"github.com/yankeguo/airmx/internal/maildir"
	"golang.org/x/crypto/bcrypt"
)

//go:embed templates
var templatesFS embed.FS

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
		tpl:          template.Must(template.New("").ParseFS(templatesFS, "templates/*.html")),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /mail/{folder}", s.handleList)
	mux.HandleFunc("GET /mail/{folder}/{id}", s.handleView)
	mux.HandleFunc("GET /mail/{folder}/{id}/html", s.handleHTML)
	mux.HandleFunc("GET /mail/{folder}/{id}/attach/{n}", s.handleAttach)
	mux.HandleFunc("POST /mail/{folder}/{id}/delete", s.handleDelete)
	s.mux = mux
	return s
}

// ListenAndServe serves the UI with HTTP Basic Auth on every request.
func (s *Server) ListenAndServe(addr string) error {
	log.Printf("web: listening on %s", addr)
	return http.ListenAndServe(addr, s.basicAuth(s.mux))
}

func (s *Server) basicAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok ||
			subtle.ConstantTimeCompare([]byte(u), []byte(s.username)) != 1 ||
			!checkPassword(s.passwordHash, p) {
			w.Header().Set("WWW-Authenticate", `Basic realm="airmx"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func checkPassword(hash []byte, password string) bool {
	return bcrypt.CompareHashAndPassword(hash, []byte(password)) == nil
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

// walkMessage classifies the entities of a message: plain text body, HTML
// body, and attachments (by walk order).
func walkMessage(raw []byte) (text string, hasHTML bool, attach []attachment, htmlBody []byte) {
	e, err := message.Read(strings.NewReader(string(raw)))
	if err != nil {
		return "", false, nil, nil
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
		case !isAttach && mt == "text/html" && htmlBody == nil:
			htmlBody, _ = io.ReadAll(part.Body)
			hasHTML = true
		case isAttach:
			if filename == "" {
				filename = fmt.Sprintf("part-%d", n)
			}
			attach = append(attach, attachment{N: n, Filename: filename, Type: mt})
			n++
		}
		return nil
	})
	return text, hasHTML, attach, htmlBody
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
	text, hasHTML, attach, _ := walkMessage(raw)
	s.render(w, "view.html", viewData{
		Folder:   string(folder),
		ID:       id,
		Subject:  decodeHeader(h.Get("Subject")),
		From:     decodeHeader(h.Get("From")),
		To:       decodeHeader(h.Get("To")),
		Date:     h.Get("Date"),
		TextBody: text,
		HasHTML:  hasHTML,
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
	_, _, _, htmlBody := walkMessage(raw)
	if htmlBody == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// No scripts, no forms, no external requests other than images/styles.
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; img-src data: https:; style-src 'unsafe-inline'")
	w.Write(htmlBody)
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

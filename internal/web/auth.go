package web

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	sessionCookieName = "airmx_session"
	sessionMaxAge     = 7 * 24 * time.Hour
)

// sessionKey derives the cookie encryption key from the configured password
// hash. Changing the password invalidates all sessions.
func (s *Server) sessionKey() []byte {
	sum := sha256.Sum256(append([]byte("airmx-session:"), s.passwordHash...))
	return sum[:]
}

// seal encrypts and authenticates a session payload for the cookie.
func (s *Server) seal(payload string) (string, error) {
	gcm, err := cipher.NewGCM(must(aes.NewCipher(s.sessionKey())))
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(
		gcm.Seal(nonce, nonce, []byte(payload), nil)), nil
}

// open decrypts and verifies a cookie value.
func (s *Server) open(value string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(must(aes.NewCipher(s.sessionKey())))
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("cookie too short")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// validSession reports whether the cookie holds a live session for the
// configured user. Payload format: "<username>|<expiry unix>".
func (s *Server) validSession(value string) bool {
	payload, err := s.open(value)
	if err != nil {
		return false
	}
	user, expiry, ok := strings.Cut(payload, "|")
	if !ok {
		return false
	}
	exp, err := strconv.ParseInt(expiry, 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(user), []byte(s.username)) == 1
}

// isHTTPS detects TLS termination, either directly or at a reverse proxy
// setting X-Forwarded-Proto, so the Secure cookie flag matches reality.
func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request) error {
	value, err := s.seal(fmt.Sprintf("%s|%d", s.username, time.Now().Add(sessionMaxAge).Unix()))
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   int(sessionMaxAge.Seconds()),
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

func (s *Server) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
}

// requireAuth redirects unauthenticated requests to the login page.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookieName)
		if err != nil || !s.validSession(c.Value) {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		s.render(w, "login.html", map[string]any{})
		return
	}
	if err := r.ParseForm(); err != nil {
		s.render(w, "login.html", map[string]any{"Error": "请求无效"})
		return
	}
	u, p := r.PostForm.Get("username"), r.PostForm.Get("password")
	if subtle.ConstantTimeCompare([]byte(u), []byte(s.username)) != 1 ||
		bcrypt.CompareHashAndPassword(s.passwordHash, []byte(p)) != nil {
		s.render(w, "login.html", map[string]any{"Error": "用户名或密码错误"})
		return
	}
	if err := s.setSessionCookie(w, r); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.clearSessionCookie(w, r)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

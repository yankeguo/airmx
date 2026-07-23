// Package maildir implements storage of received messages in maildir layout.
//
// Layout: <root>/<recipient>/<folder>/{tmp,new,cur}
// Folder is "inbox" or "spam".
package maildir

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/mail"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Folder string

const (
	FolderInbox Folder = "inbox"
	FolderSpam  Folder = "spam"
)

func (f Folder) Valid() bool { return f == FolderInbox || f == FolderSpam }

// Message is a summary of a stored message for listing.
type Message struct {
	ID          string // unique filename, safe for URLs
	Recipient   string
	Subject     string
	From        string
	Date        time.Time
	Unread      bool
	SpamStatus  string // X-Spam-Status header value, if any
	AuthResults string // Authentication-Results header value, if any
}

type Store struct {
	root string
}

func New(root string) *Store { return &Store{root: root} }

// Deliver atomically stores msg for recipient in folder and returns the
// message ID.
func (s *Store) Deliver(recipient string, folder Folder, msg []byte) (string, error) {
	if !folder.Valid() {
		return "", fmt.Errorf("invalid folder %q", folder)
	}
	recipient = strings.ToLower(strings.TrimSpace(recipient))
	if recipient == "" || strings.ContainsAny(recipient, `/\`) {
		return "", fmt.Errorf("invalid recipient %q", recipient)
	}
	base := filepath.Join(s.root, recipient, string(folder))
	for _, sub := range []string{"tmp", "new", "cur"} {
		if err := os.MkdirAll(filepath.Join(base, sub), 0o755); err != nil {
			return "", err
		}
	}
	var randPart [4]byte
	if _, err := rand.Read(randPart[:]); err != nil {
		return "", err
	}
	host, _ := os.Hostname()
	id := fmt.Sprintf("%d.%d.%s.%s", time.Now().UnixNano(), os.Getpid(), host, hex.EncodeToString(randPart[:]))

	tmpPath := filepath.Join(base, "tmp", id)
	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	if _, err := f.Write(msg); err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmpPath)
		return "", err
	}
	if err := os.Rename(tmpPath, filepath.Join(base, "new", id)); err != nil {
		os.Remove(tmpPath)
		return "", err
	}
	return id, nil
}

// List returns summaries of all messages in folder across all recipients,
// newest first.
func (s *Store) List(folder Folder) ([]Message, error) {
	if !folder.Valid() {
		return nil, fmt.Errorf("invalid folder %q", folder)
	}
	var msgs []Message
	recipients, err := os.ReadDir(s.root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	for _, rec := range recipients {
		if !rec.IsDir() {
			continue
		}
		base := filepath.Join(s.root, rec.Name(), string(folder))
		for _, sub := range []struct {
			dir    string
			unread bool
		}{{"new", true}, {"cur", false}} {
			entries, err := os.ReadDir(filepath.Join(base, sub.dir))
			if err != nil {
				continue
			}
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				m := Message{ID: e.Name(), Recipient: rec.Name(), Unread: sub.unread}
				if raw, err := os.ReadFile(filepath.Join(base, sub.dir, e.Name())); err == nil {
					fillHeaders(&m, raw)
				}
				msgs = append(msgs, m)
			}
		}
	}
	sort.Slice(msgs, func(i, j int) bool { return msgs[i].Date.After(msgs[j].Date) })
	return msgs, nil
}

func fillHeaders(m *Message, raw []byte) {
	msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		return
	}
	h := msg.Header
	m.Subject = h.Get("Subject")
	if s, err := new(mime.WordDecoder).DecodeHeader(m.Subject); err == nil {
		m.Subject = s
	}
	if addr, err := mail.ParseAddress(h.Get("From")); err == nil {
		if addr.Name != "" {
			m.From = addr.Name + " <" + addr.Address + ">"
		} else {
			m.From = addr.Address
		}
	} else {
		m.From = h.Get("From")
	}
	// Unparseable or missing Date leaves the zero time; the message then
	// sorts oldest, which is acceptable for malformed mail.
	if t, err := mail.ParseDate(h.Get("Date")); err == nil {
		m.Date = t
	}
	m.SpamStatus = h.Get("X-Spam-Status")
	m.AuthResults = h.Get("Authentication-Results")
}

// locate finds the full path of message id in folder, scanning recipients.
func (s *Store) locate(folder Folder, id string) (path string, unread bool, err error) {
	if !folder.Valid() || !validID(id) {
		return "", false, errors.New("invalid message reference")
	}
	recipients, err := os.ReadDir(s.root)
	if err != nil {
		return "", false, err
	}
	for _, rec := range recipients {
		if !rec.IsDir() {
			continue
		}
		for _, sub := range []string{"new", "cur"} {
			p := filepath.Join(s.root, rec.Name(), string(folder), sub, id)
			if _, err := os.Stat(p); err == nil {
				return p, sub == "new", nil
			}
		}
	}
	return "", false, fs.ErrNotExist
}

// Open returns the raw bytes of a message. Unread messages are moved from
// new/ to cur/ (marked as read).
func (s *Store) Open(folder Folder, id string) ([]byte, error) {
	p, unread, err := s.locate(folder, id)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	if unread {
		_ = os.Rename(p, filepath.Join(filepath.Dir(filepath.Dir(p)), "cur", id))
	}
	return raw, nil
}

// Delete removes a message.
func (s *Store) Delete(folder Folder, id string) error {
	p, _, err := s.locate(folder, id)
	if err != nil {
		return err
	}
	return os.Remove(p)
}

func validID(id string) bool {
	return id != "" && !strings.ContainsAny(id, `/\`) && id != "." && id != ".."
}

// Reader opens a message for streaming (used for attachments).
func (s *Store) Reader(folder Folder, id string) (io.ReadCloser, error) {
	p, _, err := s.locate(folder, id)
	if err != nil {
		return nil, err
	}
	return os.Open(p)
}

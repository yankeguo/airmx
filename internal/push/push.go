// Package push implements Web Push notifications for newly delivered mail:
// it persists browser push subscriptions and sends a notification to all of
// them on delivery.
package push

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
)

// notifyTimeout bounds a single push request to the vendor push service.
const notifyTimeout = 10 * time.Second

// notifyTTL is how long (seconds) the push service holds a notification
// for an offline browser before dropping it.
const notifyTTL = 24 * 3600

// Service stores push subscriptions and sends notifications to them.
type Service struct {
	file           string
	vapidPublicKey string
	vapidOptions   webpush.Options

	mu   sync.Mutex
	subs map[string]*webpush.Subscription // keyed by endpoint
}

// New loads subscriptions from file (missing file is fine) and validates
// the VAPID keys.
func New(file, vapidPublicKey, vapidPrivateKey, subscriber string) (*Service, error) {
	if vapidPublicKey == "" || vapidPrivateKey == "" {
		return nil, errors.New("vapid keys must not be empty")
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return nil, err
	}
	s := &Service{
		file:           file,
		vapidPublicKey: vapidPublicKey,
		vapidOptions: webpush.Options{
			HTTPClient:      &http.Client{Timeout: notifyTimeout},
			Subscriber:      subscriber,
			TTL:             notifyTTL,
			VAPIDPublicKey:  vapidPublicKey,
			VAPIDPrivateKey: vapidPrivateKey,
		},
		subs: make(map[string]*webpush.Subscription),
	}
	data, err := os.ReadFile(file)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		var subs []*webpush.Subscription
		if err := json.Unmarshal(data, &subs); err != nil {
			return nil, fmt.Errorf("parse %s: %w", file, err)
		}
		for _, sub := range subs {
			if sub != nil && sub.Endpoint != "" {
				s.subs[sub.Endpoint] = sub
			}
		}
	}
	return s, nil
}

// PublicKey returns the VAPID public key the browser needs to subscribe.
func (s *Service) PublicKey() string { return s.vapidPublicKey }

// Subscribe registers a subscription in its raw browser JSON form
// (PushSubscription.toJSON()).
func (s *Service) Subscribe(raw json.RawMessage) error {
	var sub webpush.Subscription
	if err := json.Unmarshal(raw, &sub); err != nil {
		return fmt.Errorf("invalid subscription: %w", err)
	}
	u, err := url.Parse(sub.Endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return errors.New("invalid subscription endpoint")
	}
	if sub.Keys.P256dh == "" || sub.Keys.Auth == "" {
		return errors.New("invalid subscription keys")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subs[sub.Endpoint] = &sub
	return s.saveLocked()
}

// Unsubscribe removes the subscription with the given endpoint.
func (s *Service) Unsubscribe(endpoint string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.subs, endpoint)
	return s.saveLocked()
}

// Count returns the number of registered subscriptions.
func (s *Service) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.subs)
}

// saveLocked persists subscriptions atomically (tmp file + rename).
func (s *Service) saveLocked() error {
	subs := make([]*webpush.Subscription, 0, len(s.subs))
	for _, sub := range s.subs {
		subs = append(subs, sub)
	}
	data, err := json.MarshalIndent(subs, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.file + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.file)
}

// Notify sends a notification to every subscription. Both title variants
// travel in the payload; the service worker picks one by the browser's
// locale. Endpoints that are gone (404/410, i.e. the user revoked the
// permission) are pruned.
func (s *Service) Notify(titleZH, titleEN, body string) {
	s.mu.Lock()
	subs := make([]*webpush.Subscription, 0, len(s.subs))
	for _, sub := range s.subs {
		subs = append(subs, sub)
	}
	s.mu.Unlock()

	if len(subs) == 0 {
		return
	}
	payload, err := json.Marshal(map[string]string{
		"title_zh": titleZH,
		"title_en": titleEN,
		"body":     body,
		"url":      "/",
	})
	if err != nil {
		return
	}
	// Send to all endpoints concurrently: each request is bounded by
	// notifyTimeout, and a slow or dead push service must not delay the
	// others.
	var wg sync.WaitGroup
	for _, sub := range subs {
		wg.Add(1)
		go func(sub *webpush.Subscription) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
			resp, err := webpush.SendNotificationWithContext(ctx, payload, sub, &s.vapidOptions)
			cancel()
			if err != nil {
				log.Printf("push: send to %s failed: %v", sub.Endpoint, err)
				return
			}
			resp.Body.Close()
			if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
				log.Printf("push: endpoint gone, removing %s", sub.Endpoint)
				_ = s.Unsubscribe(sub.Endpoint)
			} else if resp.StatusCode >= 400 {
				log.Printf("push: send to %s: status %d", sub.Endpoint, resp.StatusCode)
			}
		}(sub)
	}
	wg.Wait()
}

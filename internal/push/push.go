// Package push sends Web Push notifications (what Android and iOS show as
// system notifications for an installed web app).
package push

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/augustoroman/taskmaster/internal/store"
)

// Sender delivers push messages, signed with the server's VAPID key pair.
type Sender struct {
	publicKey, privateKey string
	// subject identifies the sender to push services: "mailto:..." or a URL.
	subject string
	client  *http.Client
}

// NewSender loads the VAPID key pair from the database, creating it on first
// use. subject is a contact for push services ("mailto:you@example.com").
func NewSender(ctx context.Context, db *store.DB, subject string) (*Sender, error) {
	s := &Sender{subject: subject, client: &http.Client{Timeout: 20 * time.Second}}
	err := db.Tx(ctx, func(tx *store.Tx) (err error) {
		if s.publicKey, err = tx.ServerKey("vapid_public"); err != nil || s.publicKey != "" {
			if err == nil {
				s.privateKey, err = tx.ServerKey("vapid_private")
			}
			return err
		}
		if s.privateKey, s.publicKey, err = webpush.GenerateVAPIDKeys(); err != nil {
			return err
		}
		if err := tx.SetServerKey("vapid_private", s.privateKey); err != nil {
			return err
		}
		return tx.SetServerKey("vapid_public", s.publicKey)
	})
	return s, err
}

// PublicKey is what browsers need to subscribe (applicationServerKey).
func (s *Sender) PublicKey() string { return s.publicKey }

// Send delivers payload to a subscription. gone means the subscription no
// longer exists (the user unsubscribed or uninstalled) and should be deleted.
func (s *Sender) Send(ctx context.Context, sub *store.PushSubscription, payload []byte, ttl time.Duration) (gone bool, err error) {
	res, err := webpush.SendNotificationWithContext(ctx, payload, &webpush.Subscription{
		Endpoint: sub.Endpoint,
		Keys:     webpush.Keys{P256dh: sub.P256dh, Auth: sub.Auth},
	}, &webpush.Options{
		HTTPClient:      s.client,
		Subscriber:      s.subject,
		VAPIDPublicKey:  s.publicKey,
		VAPIDPrivateKey: s.privateKey,
		TTL:             int(ttl.Seconds()),
		Urgency:         webpush.UrgencyNormal,
	})
	if err != nil {
		return false, err
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == http.StatusNotFound || res.StatusCode == http.StatusGone:
		return true, nil
	case res.StatusCode >= 300:
		body, _ := io.ReadAll(io.LimitReader(res.Body, 500))
		return false, fmt.Errorf("push service: %s: %s", res.Status, body)
	}
	return false, nil
}

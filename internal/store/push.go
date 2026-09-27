package store

import (
	"database/sql"
	"errors"
	"time"

	"github.com/augustoroman/taskmaster/internal/engine"
)

// PushSubscription is a device that can receive push notifications.
type PushSubscription struct {
	ID         string
	UserID     string
	Endpoint   string
	P256dh     string
	Auth       string
	UserAgent  string
	CreatedAt  time.Time
	LastUsedAt time.Time
}

// SavePushSubscription adds a subscription, or updates the one with the same
// endpoint (e.g. re-registered, possibly by another user on a shared device).
func (tx *Tx) SavePushSubscription(s *PushSubscription) error {
	if s.ID == "" {
		s.ID = NewID()
	}
	_, err := tx.exec(`
		INSERT INTO push_subscriptions (id, user_id, endpoint, p256dh, auth, user_agent, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (endpoint) DO UPDATE SET user_id = excluded.user_id, p256dh = excluded.p256dh,
			auth = excluded.auth, user_agent = excluded.user_agent`,
		s.ID, s.UserID, s.Endpoint, s.P256dh, s.Auth, s.UserAgent, ts(s.CreatedAt))
	return err
}

func (tx *Tx) PushSubscriptions(userID string) ([]*PushSubscription, error) {
	var out []*PushSubscription
	err := tx.each(`SELECT id, user_id, endpoint, p256dh, auth, user_agent, created_at, last_used_at
		FROM push_subscriptions WHERE user_id = ? ORDER BY created_at`, []any{userID},
		func(scan func(...any) error) error {
			var s PushSubscription
			var created, used string
			if err := scan(&s.ID, &s.UserID, &s.Endpoint, &s.P256dh, &s.Auth, &s.UserAgent, &created, &used); err != nil {
				return err
			}
			s.CreatedAt, s.LastUsedAt = parseTS(created), parseTS(used)
			out = append(out, &s)
			return nil
		})
	return out, err
}

// DeletePushSubscription removes a subscription by endpoint (for userID, if set).
func (tx *Tx) DeletePushSubscription(userID, endpoint string) error {
	if userID == "" {
		_, err := tx.exec(`DELETE FROM push_subscriptions WHERE endpoint = ?`, endpoint)
		return err
	}
	_, err := tx.exec(`DELETE FROM push_subscriptions WHERE endpoint = ? AND user_id = ?`, endpoint, userID)
	return err
}

func (tx *Tx) TouchPushSubscription(id string, at time.Time) error {
	_, err := tx.exec(`UPDATE push_subscriptions SET last_used_at = ? WHERE id = ?`, ts(at), id)
	return err
}

// ServerKey returns a stored server key, or "" if there is none.
func (tx *Tx) ServerKey(name string) (string, error) {
	var v string
	err := tx.queryRow(`SELECT value FROM server_keys WHERE name = ?`, name).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

func (tx *Tx) SetServerKey(name, value string) error {
	_, err := tx.exec(`INSERT INTO server_keys (name, value) VALUES (?, ?)
		ON CONFLICT (name) DO UPDATE SET value = excluded.value`, name, value)
	return err
}

// LeadReminded reports whether a lead-time reminder was sent for the occurrence.
func (tx *Tx) LeadReminded(userID, taskID string, occurrence engine.Date) (bool, error) {
	var n int
	err := tx.queryRow(`SELECT COUNT(*) FROM lead_reminders WHERE user_id = ? AND task_id = ? AND occurrence = ?`,
		userID, taskID, occurrence.String()).Scan(&n)
	return n > 0, err
}

func (tx *Tx) AddLeadReminder(userID, taskID string, occurrence engine.Date) error {
	_, err := tx.exec(`INSERT OR IGNORE INTO lead_reminders (user_id, task_id, occurrence) VALUES (?, ?, ?)`,
		userID, taskID, occurrence.String())
	return err
}

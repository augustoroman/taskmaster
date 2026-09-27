-- Keys the server generates for itself (e.g. the VAPID key pair for push).
CREATE TABLE server_keys (
  name  TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

-- A browser/device that can receive push notifications for a user.
CREATE TABLE push_subscriptions (
  id           TEXT PRIMARY KEY,
  user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  endpoint     TEXT NOT NULL UNIQUE,
  p256dh       TEXT NOT NULL,
  auth         TEXT NOT NULL,
  user_agent   TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL,
  last_used_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX push_subscriptions_user ON push_subscriptions(user_id);

-- Daily notification settings; notified_on is the (local) date of the last
-- daily notification.
ALTER TABLE users ADD COLUMN notify INTEGER NOT NULL DEFAULT 1;
ALTER TABLE users ADD COLUMN notify_time TEXT NOT NULL DEFAULT '08:00';
ALTER TABLE users ADD COLUMN notified_on TEXT NOT NULL DEFAULT '';

-- Per-person "notify me" for a tag; NULL means the default (on for your own
-- tags, off for tags shared with you).
ALTER TABLE tag_prefs ADD COLUMN notify INTEGER;

-- Lead-time reminders already sent, so each is sent once per occurrence.
CREATE TABLE lead_reminders (
  user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  task_id    TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  occurrence TEXT NOT NULL,
  PRIMARY KEY (user_id, task_id, occurrence)
);

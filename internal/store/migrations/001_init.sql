-- Dates are 'YYYY-MM-DD' text ('' = none); timestamps are RFC 3339 UTC text.

CREATE TABLE users (
  id            TEXT PRIMARY KEY,
  email         TEXT NOT NULL UNIQUE,
  name          TEXT NOT NULL DEFAULT '',
  picture       TEXT NOT NULL DEFAULT '',
  tz            TEXT NOT NULL DEFAULT '',
  created_at    TEXT NOT NULL,
  last_login_at TEXT NOT NULL
);

CREATE TABLE tags (
  id         TEXT PRIMARY KEY,
  owner_id   TEXT NOT NULL REFERENCES users(id),
  name       TEXT NOT NULL,
  color      TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  UNIQUE (owner_id, name)
);

-- user_id is NULL until the invited email first logs in.
CREATE TABLE tag_shares (
  id         TEXT PRIMARY KEY,
  tag_id     TEXT NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
  email      TEXT NOT NULL,
  user_id    TEXT REFERENCES users(id),
  level      INTEGER NOT NULL CHECK (level BETWEEN 1 AND 3),
  created_by TEXT NOT NULL REFERENCES users(id),
  created_at TEXT NOT NULL,
  UNIQUE (tag_id, email)
);
CREATE INDEX tag_shares_user ON tag_shares(user_id);
CREATE INDEX tag_shares_email ON tag_shares(email);

CREATE TABLE tag_prefs (
  user_id TEXT NOT NULL REFERENCES users(id),
  tag_id  TEXT NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
  hidden  INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (user_id, tag_id)
);

CREATE TABLE tasks (
  id              TEXT PRIMARY KEY,
  creator_id      TEXT NOT NULL REFERENCES users(id),
  title           TEXT NOT NULL,
  description     TEXT NOT NULL DEFAULT '',
  priority        INTEGER NOT NULL,
  lead_days       INTEGER NOT NULL DEFAULT 0,
  tz              TEXT NOT NULL,
  kind            TEXT NOT NULL,
  interval_n      INTEGER NOT NULL DEFAULT 0,
  interval_unit   TEXT NOT NULL DEFAULT '',
  rrule           TEXT NOT NULL DEFAULT '',
  rrule_start     TEXT NOT NULL DEFAULT '',
  -- Current occurrence (engine.State).
  due             TEXT NOT NULL DEFAULT '',
  deferred        INTEGER NOT NULL DEFAULT 0,
  deferred_from   TEXT NOT NULL DEFAULT '',
  paused          INTEGER NOT NULL DEFAULT 0,
  pause_until     TEXT NOT NULL DEFAULT '',
  current_slot_id TEXT NOT NULL DEFAULT '',
  done            INTEGER NOT NULL DEFAULT 0,
  archived_at     TEXT NOT NULL DEFAULT '',
  version         INTEGER NOT NULL DEFAULT 1,
  created_at      TEXT NOT NULL,
  updated_at      TEXT NOT NULL
);
CREATE INDEX tasks_creator ON tasks(creator_id);

CREATE TABLE task_tags (
  task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  tag_id  TEXT NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
  PRIMARY KEY (task_id, tag_id)
);
CREATE INDEX task_tags_tag ON task_tags(tag_id);

CREATE TABLE cycle_slots (
  id          TEXT PRIMARY KEY,
  task_id     TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  position    INTEGER NOT NULL,
  title       TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  -- Removed slots are kept so history can still name them.
  removed     INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX cycle_slots_task ON cycle_slots(task_id);

CREATE TABLE checklist_items (
  id       TEXT PRIMARY KEY,
  task_id  TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  position INTEGER NOT NULL,
  title    TEXT NOT NULL,
  removed  INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX checklist_items_task ON checklist_items(task_id);

-- Checks for the current occurrence only; cleared when it ends.
CREATE TABLE checklist_checks (
  task_id    TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  item_id    TEXT NOT NULL REFERENCES checklist_items(id) ON DELETE CASCADE,
  user_id    TEXT NOT NULL REFERENCES users(id),
  date       TEXT NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY (task_id, item_id)
);

CREATE TABLE events (
  id         TEXT PRIMARY KEY,
  task_id    TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  kind       TEXT NOT NULL,
  user_id    TEXT REFERENCES users(id), -- NULL for system events
  date       TEXT NOT NULL,
  occurrence TEXT NOT NULL DEFAULT '',
  slot_id    TEXT NOT NULL DEFAULT '',
  note       TEXT NOT NULL DEFAULT '',
  data       TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL,
  edited_at  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX events_task ON events(task_id, date, created_at);

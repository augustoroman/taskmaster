package store

import (
	"database/sql"
	"time"
)

// Level is an access level. Levels compare with < and >.
type Level int

const (
	LevelNone Level = 0
	LevelRead Level = 1
	LevelDo   Level = 2
	LevelFull Level = 3
)

type Tag struct {
	ID      string
	OwnerID string
	Name    string
	// Color is the owner's color for the tag, "#rrggbb".
	Color     string
	CreatedAt time.Time
}

// UserTag is a tag as one user sees it. Its Color is theirs: their own choice
// if they made one, else the color it was shared with, else the owner's.
type UserTag struct {
	Tag
	Level  Level
	Hidden bool
}

type Share struct {
	ID        string
	TagID     string
	Email     string
	UserID    string // "" until the invitee logs in
	Level     Level
	CreatedBy string
	CreatedAt time.Time
	// Color is the sharer's color when shared: the recipient's starting color.
	Color string
}

func (tx *Tx) InsertTag(t *Tag) error {
	if t.ID == "" {
		t.ID = NewID()
	}
	_, err := tx.exec(`INSERT INTO tags (id, owner_id, name, color, created_at) VALUES (?, ?, ?, ?, ?)`,
		t.ID, t.OwnerID, t.Name, t.Color, ts(t.CreatedAt))
	return err
}

func (tx *Tx) GetTag(id string) (*Tag, error) {
	var t Tag
	var created string
	err := tx.queryRow(`SELECT id, owner_id, name, color, created_at FROM tags WHERE id = ?`, id).
		Scan(&t.ID, &t.OwnerID, &t.Name, &t.Color, &created)
	if err != nil {
		return nil, notFound(err)
	}
	t.CreatedAt = parseTS(created)
	return &t, nil
}

func (tx *Tx) UpdateTag(t *Tag) error {
	_, err := tx.exec(`UPDATE tags SET name = ?, color = ? WHERE id = ?`, t.Name, t.Color, t.ID)
	return err
}

func (tx *Tx) DeleteTag(id string) error {
	_, err := tx.exec(`DELETE FROM tags WHERE id = ?`, id)
	return err
}

// TagLevel is userID's access to a tag: full for its owner, else their share.
func (tx *Tx) TagLevel(userID, tagID string) (Level, error) {
	var level Level
	err := tx.queryRow(`
		SELECT CASE WHEN g.owner_id = ?1 THEN 3 ELSE COALESCE(s.level, 0) END
		FROM tags g LEFT JOIN tag_shares s ON s.tag_id = g.id AND s.user_id = ?1
		WHERE g.id = ?2`, userID, tagID).Scan(&level)
	return level, notFound(err)
}

// UserTags lists the tags userID owns or has been shared.
func (tx *Tx) UserTags(userID string) ([]UserTag, error) {
	rows, err := tx.query(`
		SELECT g.id, g.owner_id, g.name,
		       CASE WHEN g.owner_id = ?1 THEN g.color
		            ELSE COALESCE(NULLIF(p.color, ''), NULLIF(s.color, ''), g.color) END,
		       g.created_at,
		       CASE WHEN g.owner_id = ?1 THEN 3 ELSE s.level END,
		       COALESCE(p.hidden, 0)
		FROM tags g
		LEFT JOIN tag_shares s ON s.tag_id = g.id AND s.user_id = ?1
		LEFT JOIN tag_prefs p ON p.tag_id = g.id AND p.user_id = ?1
		WHERE g.owner_id = ?1 OR s.user_id IS NOT NULL
		ORDER BY g.name COLLATE NOCASE, g.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UserTag
	for rows.Next() {
		var t UserTag
		var created string
		if err := rows.Scan(&t.ID, &t.OwnerID, &t.Name, &t.Color, &created, &t.Level, &t.Hidden); err != nil {
			return nil, err
		}
		t.CreatedAt = parseTS(created)
		out = append(out, t)
	}
	return out, rows.Err()
}

// SetTagColor sets userID's own color for a tag they don't own. (Owners
// change the tag's color with UpdateTag.)
func (tx *Tx) SetTagColor(userID, tagID, color string) error {
	_, err := tx.exec(`
		INSERT INTO tag_prefs (user_id, tag_id, color) VALUES (?, ?, ?)
		ON CONFLICT (user_id, tag_id) DO UPDATE SET color = excluded.color`, userID, tagID, color)
	return err
}

func (tx *Tx) SetTagHidden(userID, tagID string, hidden bool) error {
	_, err := tx.exec(`
		INSERT INTO tag_prefs (user_id, tag_id, hidden) VALUES (?, ?, ?)
		ON CONFLICT (user_id, tag_id) DO UPDATE SET hidden = excluded.hidden`, userID, tagID, hidden)
	return err
}

const shareCols = `id, tag_id, email, COALESCE(user_id, ''), level, created_by, created_at, color`

func scanShare(row interface{ Scan(...any) error }) (*Share, error) {
	var s Share
	var created string
	if err := row.Scan(&s.ID, &s.TagID, &s.Email, &s.UserID, &s.Level, &s.CreatedBy, &created, &s.Color); err != nil {
		return nil, notFound(err)
	}
	s.CreatedAt = parseTS(created)
	return &s, nil
}

func (tx *Tx) GetShare(id string) (*Share, error) {
	return scanShare(tx.queryRow(`SELECT `+shareCols+` FROM tag_shares WHERE id = ?`, id))
}

func (tx *Tx) GetShareByEmail(tagID, email string) (*Share, error) {
	return scanShare(tx.queryRow(`SELECT `+shareCols+` FROM tag_shares WHERE tag_id = ? AND email = ?`, tagID, NormalizeEmail(email)))
}

func (tx *Tx) ListShares(tagID string) ([]*Share, error) {
	rows, err := tx.query(`SELECT `+shareCols+` FROM tag_shares WHERE tag_id = ? ORDER BY email`, tagID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Share
	for rows.Next() {
		s, err := scanShare(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (tx *Tx) InsertShare(s *Share) error {
	if s.ID == "" {
		s.ID = NewID()
	}
	s.Email = NormalizeEmail(s.Email)
	_, err := tx.exec(`INSERT INTO tag_shares (id, tag_id, email, user_id, level, created_by, created_at, color) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.TagID, s.Email, nullable(s.UserID), s.Level, s.CreatedBy, ts(s.CreatedAt), s.Color)
	return err
}

func (tx *Tx) UpdateShareLevel(id string, level Level) error {
	_, err := tx.exec(`UPDATE tag_shares SET level = ? WHERE id = ?`, level, id)
	return err
}

func (tx *Tx) DeleteShare(id string) error {
	_, err := tx.exec(`DELETE FROM tag_shares WHERE id = ?`, id)
	return err
}

// HasPendingShare reports whether email has been invited to any tag.
func (tx *Tx) HasPendingShare(email string) (bool, error) {
	var n int
	err := tx.queryRow(`SELECT COUNT(*) FROM tag_shares WHERE email = ? AND user_id IS NULL`, NormalizeEmail(email)).Scan(&n)
	return n > 0, err
}

// ClaimShares attaches shares addressed to email to userID.
func (tx *Tx) ClaimShares(email, userID string) error {
	_, err := tx.exec(`UPDATE tag_shares SET user_id = ? WHERE email = ? AND user_id IS NULL`, userID, NormalizeEmail(email))
	return err
}

func nullable(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

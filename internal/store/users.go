package store

import (
	"strings"
	"time"
)

type User struct {
	ID          string
	Email       string // lowercase
	Name        string
	Picture     string
	TZ          string // IANA; "" until set
	CreatedAt   time.Time
	LastLoginAt time.Time
}

const userCols = `id, email, name, picture, tz, created_at, last_login_at`

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	var created, login string
	if err := row.Scan(&u.ID, &u.Email, &u.Name, &u.Picture, &u.TZ, &created, &login); err != nil {
		return nil, notFound(err)
	}
	u.CreatedAt, u.LastLoginAt = parseTS(created), parseTS(login)
	return &u, nil
}

func NormalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

func (tx *Tx) GetUser(id string) (*User, error) {
	return scanUser(tx.queryRow(`SELECT `+userCols+` FROM users WHERE id = ?`, id))
}

func (tx *Tx) GetUserByEmail(email string) (*User, error) {
	return scanUser(tx.queryRow(`SELECT `+userCols+` FROM users WHERE email = ?`, NormalizeEmail(email)))
}

// GetUsers returns the users with the given IDs, keyed by ID.
func (tx *Tx) GetUsers(ids []string) (map[string]*User, error) {
	out := map[string]*User{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := tx.query(`SELECT `+userCols+` FROM users WHERE id IN (`+placeholders(len(ids))+`)`, anys(ids)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out[u.ID] = u
	}
	return out, rows.Err()
}

func (tx *Tx) InsertUser(u *User) error {
	if u.ID == "" {
		u.ID = NewID()
	}
	u.Email = NormalizeEmail(u.Email)
	_, err := tx.exec(`INSERT INTO users (`+userCols+`) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		u.ID, u.Email, u.Name, u.Picture, u.TZ, ts(u.CreatedAt), ts(u.LastLoginAt))
	return err
}

func (tx *Tx) UpdateUser(u *User) error {
	_, err := tx.exec(`UPDATE users SET name = ?, picture = ?, tz = ?, last_login_at = ? WHERE id = ?`,
		u.Name, u.Picture, u.TZ, ts(u.LastLoginAt), u.ID)
	return err
}

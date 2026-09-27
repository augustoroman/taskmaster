package store

import (
	"strings"
	"time"

	"github.com/augustoroman/taskmaster/internal/engine"
)

type User struct {
	ID          string
	Email       string // lowercase
	Name        string
	Picture     string
	TZ          string // IANA; "" until set
	CreatedAt   time.Time
	LastLoginAt time.Time
	// Notify turns daily notifications on; NotifyTime is when ("15:04", in
	// TZ); NotifiedOn is the local date they were last sent.
	Notify     bool
	NotifyTime string
	NotifiedOn engine.Date
}

const userCols = `id, email, name, picture, tz, created_at, last_login_at, notify, notify_time, notified_on`

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	var created, login, notified string
	if err := row.Scan(&u.ID, &u.Email, &u.Name, &u.Picture, &u.TZ, &created, &login, &u.Notify, &u.NotifyTime, &notified); err != nil {
		return nil, notFound(err)
	}
	u.CreatedAt, u.LastLoginAt, u.NotifiedOn = parseTS(created), parseTS(login), mustDate(notified)
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
	if u.NotifyTime == "" {
		u.Notify, u.NotifyTime = true, "08:00"
	}
	_, err := tx.exec(`INSERT INTO users (`+userCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ID, u.Email, u.Name, u.Picture, u.TZ, ts(u.CreatedAt), ts(u.LastLoginAt), u.Notify, u.NotifyTime, u.NotifiedOn.String())
	return err
}

func (tx *Tx) UpdateUser(u *User) error {
	_, err := tx.exec(`UPDATE users SET name = ?, picture = ?, tz = ?, last_login_at = ?, notify = ?, notify_time = ?, notified_on = ? WHERE id = ?`,
		u.Name, u.Picture, u.TZ, ts(u.LastLoginAt), u.Notify, u.NotifyTime, u.NotifiedOn.String(), u.ID)
	return err
}

// AllUsers lists every user.
func (tx *Tx) AllUsers() ([]*User, error) {
	var out []*User
	err := tx.each(`SELECT `+userCols+` FROM users ORDER BY id`, nil, func(scan func(...any) error) error {
		u, err := scanUser(scanner(scan))
		if err != nil {
			return err
		}
		out = append(out, u)
		return nil
	})
	return out, err
}

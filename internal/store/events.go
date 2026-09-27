package store

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/augustoroman/taskmaster/internal/engine"
)

// Event kinds beyond the engine's.
const (
	EventNote            engine.EventKind = "note"
	EventScheduleChanged engine.EventKind = "schedule_changed"
)

type Event struct {
	ID         string
	TaskID     string
	Kind       engine.EventKind
	UserID     string // "" for system events
	Date       engine.Date
	Occurrence engine.Date
	SlotID     string
	Note       string // markdown
	Data       EventData
	CreatedAt  time.Time
	EditedAt   time.Time
}

// EventData holds the kind-specific details.
type EventData struct {
	Merged    bool        `json:"merged,omitempty"`
	Checked   []string    `json:"checked,omitempty"`
	Unchecked []string    `json:"unchecked,omitempty"`
	From      engine.Date `json:"from,omitzero"`
	To        engine.Date `json:"to,omitzero"`
	// ItemID is the checklist item a note is about.
	ItemID string `json:"item_id,omitempty"`
}

// FromEngine converts an engine event.
func FromEngine(taskID, userID string, e engine.Event, now time.Time) *Event {
	ev := &Event{
		ID:         NewID(),
		TaskID:     taskID,
		Kind:       e.Kind,
		UserID:     userID,
		Date:       e.Date,
		Occurrence: e.Occurrence,
		SlotID:     string(e.Slot),
		CreatedAt:  now,
		Data:       EventData{Merged: e.Merged, From: e.From, To: e.To},
	}
	for _, id := range e.Checked {
		ev.Data.Checked = append(ev.Data.Checked, string(id))
	}
	for _, id := range e.Unchecked {
		ev.Data.Unchecked = append(ev.Data.Unchecked, string(id))
	}
	return ev
}

const eventCols = `id, task_id, kind, COALESCE(user_id, ''), date, occurrence, slot_id, note, data, created_at, edited_at`

func scanEvent(row interface{ Scan(...any) error }) (*Event, error) {
	var e Event
	var date, occ, data, created, edited string
	err := row.Scan(&e.ID, &e.TaskID, &e.Kind, &e.UserID, &date, &occ, &e.SlotID, &e.Note, &data, &created, &edited)
	if err != nil {
		return nil, notFound(err)
	}
	e.Date, e.Occurrence = mustDate(date), mustDate(occ)
	e.CreatedAt, e.EditedAt = parseTS(created), parseTS(edited)
	if err := json.Unmarshal([]byte(data), &e.Data); err != nil {
		return nil, fmt.Errorf("event %s: corrupt data: %w", e.ID, err)
	}
	return &e, nil
}

func (tx *Tx) InsertEvent(e *Event) error {
	if e.ID == "" {
		e.ID = NewID()
	}
	data, err := json.Marshal(e.Data)
	if err != nil {
		return err
	}
	_, err = tx.exec(`INSERT INTO events (id, task_id, kind, user_id, date, occurrence, slot_id, note, data, created_at, edited_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.TaskID, e.Kind, nullable(e.UserID), e.Date.String(), e.Occurrence.String(), e.SlotID, e.Note,
		string(data), ts(e.CreatedAt), ts(e.EditedAt))
	return err
}

func (tx *Tx) GetEvent(id string) (*Event, error) {
	return scanEvent(tx.queryRow(`SELECT `+eventCols+` FROM events WHERE id = ?`, id))
}

// UpdateEvent saves an event's kind, user, date, note and edited time.
func (tx *Tx) UpdateEvent(e *Event) error {
	_, err := tx.exec(`UPDATE events SET kind = ?, user_id = ?, date = ?, note = ?, edited_at = ? WHERE id = ?`,
		e.Kind, nullable(e.UserID), e.Date.String(), e.Note, ts(e.EditedAt), e.ID)
	return err
}

func (tx *Tx) DeleteEvent(id string) error {
	_, err := tx.exec(`DELETE FROM events WHERE id = ?`, id)
	return err
}

// ListEvents returns a task's events newest first (by date, then creation).
// slotID, if set, limits them to one cycle slot. pageToken continues a
// previous call; the returned token is "" on the last page.
func (tx *Tx) ListEvents(taskID, slotID string, limit int, pageToken string) ([]*Event, string, error) {
	where := `task_id = ?`
	args := []any{taskID}
	if slotID != "" {
		where += ` AND slot_id = ?`
		args = append(args, slotID)
	}
	if pageToken != "" {
		raw, err := base64.RawURLEncoding.DecodeString(pageToken)
		parts := strings.Split(string(raw), "|")
		if err != nil || len(parts) != 3 {
			return nil, "", fmt.Errorf("invalid page token")
		}
		where += ` AND (date, created_at, id) < (?, ?, ?)`
		args = append(args, parts[0], parts[1], parts[2])
	}
	args = append(args, limit+1)
	var events []*Event
	err := tx.each(`SELECT `+eventCols+` FROM events WHERE `+where+` ORDER BY date DESC, created_at DESC, id DESC LIMIT ?`, args,
		func(scan func(...any) error) error {
			e, err := scanEvent(scanner(scan))
			if err != nil {
				return err
			}
			events = append(events, e)
			return nil
		})
	if err != nil || len(events) <= limit {
		return events, "", err
	}
	events = events[:limit]
	last := events[limit-1]
	token := base64.RawURLEncoding.EncodeToString([]byte(last.Date.String() + "|" + ts(last.CreatedAt) + "|" + last.ID))
	return events, token, nil
}

type scanner func(...any) error

func (s scanner) Scan(dest ...any) error { return s(dest...) }

// LastDoneDates returns the latest done date of each task that has one.
func (tx *Tx) LastDoneDates(taskIDs []string) (map[string]engine.Date, error) {
	out := map[string]engine.Date{}
	if len(taskIDs) == 0 {
		return out, nil
	}
	args := append(anys(taskIDs), engine.EventDone)
	err := tx.each(`SELECT task_id, MAX(date) FROM events WHERE task_id IN (`+placeholders(len(taskIDs))+`) AND kind = ? GROUP BY task_id`, args,
		func(scan func(...any) error) error {
			var id, date string
			if err := scan(&id, &date); err != nil {
				return err
			}
			out[id] = mustDate(date)
			return nil
		})
	return out, err
}

// TasksWithHistory returns which of the tasks have any events besides notes
// and schedule changes (i.e. they've been done, missed, deferred, ...).
func (tx *Tx) TasksWithHistory(taskIDs []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(taskIDs) == 0 {
		return out, nil
	}
	args := append(anys(taskIDs), EventNote, EventScheduleChanged)
	err := tx.each(`SELECT DISTINCT task_id FROM events WHERE task_id IN (`+placeholders(len(taskIDs))+`) AND kind NOT IN (?, ?)`, args,
		func(scan func(...any) error) error {
			var id string
			if err := scan(&id); err != nil {
				return err
			}
			out[id] = true
			return nil
		})
	return out, err
}

// LastDone returns the date of the task's latest done event, or zero.
func (tx *Tx) LastDone(taskID string) (engine.Date, error) {
	var date string
	err := tx.queryRow(`SELECT COALESCE(MAX(date), '') FROM events WHERE task_id = ? AND kind = ?`, taskID, engine.EventDone).Scan(&date)
	if err != nil {
		return engine.Date{}, err
	}
	return mustDate(date), nil
}

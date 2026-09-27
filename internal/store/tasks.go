package store

import (
	"time"

	"github.com/augustoroman/taskmaster/internal/engine"
)

type Task struct {
	ID          string
	CreatorID   string
	Title       string
	Description string // markdown
	Priority    engine.Priority
	LeadDays    int
	TZ          string
	Kind        engine.Kind
	Interval    engine.Interval
	RRule       string
	RRuleStart  engine.Date
	// State is the current occurrence, including checklist checks.
	State engine.State
	// CheckedBy records who checked each item in State.Checks.
	CheckedBy  map[engine.ItemID]string
	ArchivedAt time.Time
	Version    int64
	CreatedAt  time.Time
	UpdatedAt  time.Time
	// Slots and Checklist are in order, including removed entries (kept so
	// history can name them).
	Slots     []Slot
	Checklist []ChecklistItem
	TagIDs    []string
}

type Slot struct {
	ID          string
	Title       string
	Description string
	Removed     bool
}

type ChecklistItem struct {
	ID      string
	Title   string
	Removed bool
}

// Engine returns the task's scheduling definition.
func (t *Task) Engine() (engine.Task, error) {
	def := engine.Task{
		Kind:     t.Kind,
		Interval: t.Interval,
		Priority: t.Priority,
		Lead:     t.LeadDays,
	}
	if t.Kind == engine.KindFixed || t.Kind == engine.KindCycle {
		r, err := engine.ParseRecurrence(t.RRule, t.RRuleStart)
		if err != nil {
			return def, err
		}
		def.Recurrence = r
	}
	for _, s := range t.Slots {
		if !s.Removed {
			def.Slots = append(def.Slots, engine.SlotID(s.ID))
		}
	}
	for _, item := range t.Checklist {
		if !item.Removed {
			def.Checklist = append(def.Checklist, engine.ItemID(item.ID))
		}
	}
	return def, def.Validate()
}

const taskCols = `id, creator_id, title, description, priority, lead_days, tz, kind, interval_n, interval_unit,
	rrule, rrule_start, due, deferred, deferred_from, paused, pause_until, current_slot_id, done,
	archived_at, version, created_at, updated_at`

func scanTask(row interface{ Scan(...any) error }) (*Task, error) {
	var t Task
	var rruleStart, due, deferredFrom, pauseUntil, slot, archived, created, updated string
	err := row.Scan(&t.ID, &t.CreatorID, &t.Title, &t.Description, &t.Priority, &t.LeadDays, &t.TZ, &t.Kind,
		&t.Interval.N, &t.Interval.Unit, &t.RRule, &rruleStart, &due, &t.State.Deferred, &deferredFrom,
		&t.State.Paused, &pauseUntil, &slot, &t.State.Done, &archived, &t.Version, &created, &updated)
	if err != nil {
		return nil, notFound(err)
	}
	t.RRuleStart = mustDate(rruleStart)
	t.State.Due = mustDate(due)
	t.State.DeferredFrom = mustDate(deferredFrom)
	t.State.PauseUntil = mustDate(pauseUntil)
	t.State.Slot = engine.SlotID(slot)
	t.ArchivedAt, t.CreatedAt, t.UpdatedAt = parseTS(archived), parseTS(created), parseTS(updated)
	return &t, nil
}

func mustDate(s string) engine.Date {
	d, err := engine.ParseDate(s)
	if err != nil {
		panic("corrupt date in database: " + err.Error())
	}
	return d
}

func (tx *Tx) GetTask(id string) (*Task, error) {
	t, err := scanTask(tx.queryRow(`SELECT `+taskCols+` FROM tasks WHERE id = ?`, id))
	if err != nil {
		return nil, err
	}
	return t, tx.hydrate([]*Task{t})
}

// GetTasks returns the tasks with the given IDs, in no particular order.
func (tx *Tx) GetTasks(ids []string) ([]*Task, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := tx.query(`SELECT `+taskCols+` FROM tasks WHERE id IN (`+placeholders(len(ids))+`)`, anys(ids)...)
	if err != nil {
		return nil, err
	}
	var tasks []*Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		tasks = append(tasks, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return tasks, tx.hydrate(tasks)
}

// hydrate loads slots, checklist items, checks and tags for tasks.
func (tx *Tx) hydrate(tasks []*Task) error {
	byID := map[string]*Task{}
	ids := make([]any, len(tasks))
	for i, t := range tasks {
		byID[t.ID] = t
		ids[i] = t.ID
		t.Slots, t.Checklist, t.TagIDs = nil, nil, nil
		t.State.Checks, t.CheckedBy = nil, nil
	}
	in := `(` + placeholders(len(ids)) + `)`

	err := tx.each(`SELECT task_id, id, title, description, removed FROM cycle_slots WHERE task_id IN `+in+` ORDER BY position`, ids,
		func(scan func(...any) error) error {
			var taskID string
			var s Slot
			if err := scan(&taskID, &s.ID, &s.Title, &s.Description, &s.Removed); err != nil {
				return err
			}
			byID[taskID].Slots = append(byID[taskID].Slots, s)
			return nil
		})
	if err != nil {
		return err
	}
	err = tx.each(`SELECT task_id, id, title, removed FROM checklist_items WHERE task_id IN `+in+` ORDER BY position`, ids,
		func(scan func(...any) error) error {
			var taskID string
			var item ChecklistItem
			if err := scan(&taskID, &item.ID, &item.Title, &item.Removed); err != nil {
				return err
			}
			byID[taskID].Checklist = append(byID[taskID].Checklist, item)
			return nil
		})
	if err != nil {
		return err
	}
	err = tx.each(`SELECT task_id, item_id, user_id, date FROM checklist_checks WHERE task_id IN `+in, ids,
		func(scan func(...any) error) error {
			var taskID, itemID, userID, date string
			if err := scan(&taskID, &itemID, &userID, &date); err != nil {
				return err
			}
			t := byID[taskID]
			if t.State.Checks == nil {
				t.State.Checks, t.CheckedBy = map[engine.ItemID]engine.Date{}, map[engine.ItemID]string{}
			}
			t.State.Checks[engine.ItemID(itemID)] = mustDate(date)
			t.CheckedBy[engine.ItemID(itemID)] = userID
			return nil
		})
	if err != nil {
		return err
	}
	return tx.each(`SELECT task_id, tag_id FROM task_tags WHERE task_id IN `+in+` ORDER BY tag_id`, ids,
		func(scan func(...any) error) error {
			var taskID, tagID string
			if err := scan(&taskID, &tagID); err != nil {
				return err
			}
			byID[taskID].TagIDs = append(byID[taskID].TagIDs, tagID)
			return nil
		})
}

func (tx *Tx) each(query string, args []any, fn func(scan func(...any) error) error) error {
	rows, err := tx.query(query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := fn(rows.Scan); err != nil {
			return err
		}
	}
	return rows.Err()
}

// InsertTask inserts a new task with its slots, checklist, checks and tags.
func (tx *Tx) InsertTask(t *Task) error {
	if t.ID == "" {
		t.ID = NewID()
	}
	t.Version = 1
	_, err := tx.exec(`INSERT INTO tasks (`+taskCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.ID, t.CreatorID, t.Title, t.Description, t.Priority, t.LeadDays, t.TZ, t.Kind, t.Interval.N, t.Interval.Unit,
		t.RRule, t.RRuleStart.String(), t.State.Due.String(), t.State.Deferred, t.State.DeferredFrom.String(),
		t.State.Paused, t.State.PauseUntil.String(), string(t.State.Slot), t.State.Done,
		ts(t.ArchivedAt), t.Version, ts(t.CreatedAt), ts(t.UpdatedAt))
	if err != nil {
		return err
	}
	return tx.writeChildren(t)
}

// UpdateTask saves all of t if its version is still t.Version, then
// increments t.Version. It returns ErrConflict if the task changed.
func (tx *Tx) UpdateTask(t *Task) error {
	res, err := tx.exec(`
		UPDATE tasks SET title = ?, description = ?, priority = ?, lead_days = ?, tz = ?, kind = ?,
			interval_n = ?, interval_unit = ?, rrule = ?, rrule_start = ?, due = ?, deferred = ?,
			deferred_from = ?, paused = ?, pause_until = ?, current_slot_id = ?, done = ?, archived_at = ?,
			updated_at = ?, version = version + 1
		WHERE id = ? AND version = ?`,
		t.Title, t.Description, t.Priority, t.LeadDays, t.TZ, t.Kind, t.Interval.N, t.Interval.Unit,
		t.RRule, t.RRuleStart.String(), t.State.Due.String(), t.State.Deferred, t.State.DeferredFrom.String(),
		t.State.Paused, t.State.PauseUntil.String(), string(t.State.Slot), t.State.Done, ts(t.ArchivedAt),
		ts(t.UpdatedAt), t.ID, t.Version)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrConflict
	}
	t.Version++
	return tx.writeChildren(t)
}

func (tx *Tx) writeChildren(t *Task) error {
	for i, s := range t.Slots {
		_, err := tx.exec(`
			INSERT INTO cycle_slots (id, task_id, position, title, description, removed) VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT (id) DO UPDATE SET position = excluded.position, title = excluded.title,
				description = excluded.description, removed = excluded.removed`,
			s.ID, t.ID, i, s.Title, s.Description, s.Removed)
		if err != nil {
			return err
		}
	}
	for i, item := range t.Checklist {
		_, err := tx.exec(`
			INSERT INTO checklist_items (id, task_id, position, title, removed) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (id) DO UPDATE SET position = excluded.position, title = excluded.title, removed = excluded.removed`,
			item.ID, t.ID, i, item.Title, item.Removed)
		if err != nil {
			return err
		}
	}
	if _, err := tx.exec(`DELETE FROM checklist_checks WHERE task_id = ?`, t.ID); err != nil {
		return err
	}
	for item, date := range t.State.Checks {
		_, err := tx.exec(`INSERT INTO checklist_checks (task_id, item_id, user_id, date, created_at) VALUES (?, ?, ?, ?, ?)`,
			t.ID, string(item), t.CheckedBy[item], date.String(), ts(t.UpdatedAt))
		if err != nil {
			return err
		}
	}
	if _, err := tx.exec(`DELETE FROM task_tags WHERE task_id = ?`, t.ID); err != nil {
		return err
	}
	for _, tag := range t.TagIDs {
		if _, err := tx.exec(`INSERT INTO task_tags (task_id, tag_id) VALUES (?, ?)`, t.ID, tag); err != nil {
			return err
		}
	}
	return nil
}

func (tx *Tx) DeleteTask(id string) error {
	_, err := tx.exec(`DELETE FROM tasks WHERE id = ?`, id)
	return err
}

// accessQuery computes each task's access level for user ?1: full for its
// creator, else the highest level among its tags (full for tags they own).
const accessQuery = `
	SELECT t.id, CASE WHEN t.creator_id = ?1 THEN 3 ELSE COALESCE((
		SELECT MAX(CASE WHEN g.owner_id = ?1 THEN 3 ELSE COALESCE(s.level, 0) END)
		FROM task_tags tt JOIN tags g ON g.id = tt.tag_id
		LEFT JOIN tag_shares s ON s.tag_id = g.id AND s.user_id = ?1
		WHERE tt.task_id = t.id), 0) END AS level
	FROM tasks t`

// TaskLevel is userID's access to a task (LevelNone if none or no such task).
func (tx *Tx) TaskLevel(userID, taskID string) (Level, error) {
	var level Level
	err := tx.queryRow(`SELECT level FROM (`+accessQuery+` WHERE t.id = ?2)`, userID, taskID).Scan(&level)
	if err != nil {
		if notFound(err) == ErrNotFound {
			return LevelNone, nil
		}
		return LevelNone, err
	}
	return level, nil
}

// VisibleTasks returns the IDs of all tasks userID can see, with their levels.
func (tx *Tx) VisibleTasks(userID string) (map[string]Level, error) {
	out := map[string]Level{}
	err := tx.each(`SELECT id, level FROM (`+accessQuery+`) WHERE level > 0`, []any{userID},
		func(scan func(...any) error) error {
			var id string
			var level Level
			if err := scan(&id, &level); err != nil {
				return err
			}
			out[id] = level
			return nil
		})
	return out, err
}

// ActiveTaskIDs lists tasks the sweep should look at: not done and not
// archived.
func (tx *Tx) ActiveTaskIDs() ([]string, error) {
	var ids []string
	err := tx.each(`SELECT id FROM tasks WHERE done = 0 AND archived_at = ''`, nil,
		func(scan func(...any) error) error {
			var id string
			if err := scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
			return nil
		})
	return ids, err
}

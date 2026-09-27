package app

import (
	"context"
	"strings"

	"github.com/augustoroman/taskmaster/internal/engine"
	"github.com/augustoroman/taskmaster/internal/store"
)

const (
	defaultEventPage = 50
	maxEventPage     = 500
)

// ListEvents returns a task's history, newest first.
func (s *Service) ListEvents(ctx context.Context, u *store.User, taskID, slotID string, limit int, pageToken string) ([]*EventView, string, error) {
	if limit <= 0 {
		limit = defaultEventPage
	}
	limit = min(limit, maxEventPage)
	var views []*EventView
	var next string
	err := s.db.Tx(ctx, func(tx *store.Tx) error {
		if _, err := s.loadTask(tx, u, taskID, store.LevelRead); err != nil {
			return err
		}
		events, token, err := tx.ListEvents(taskID, slotID, limit, pageToken)
		if err != nil {
			return invalid("%v", err)
		}
		next = token
		views, err = s.eventViews(tx, events)
		return err
	})
	return views, next, err
}

// AddNote adds a standalone note, dated today unless date is set.
func (s *Service) AddNote(ctx context.Context, u *store.User, taskID, note string, date engine.Date) (*EventView, error) {
	note = strings.TrimSpace(note)
	if note == "" {
		return nil, invalid("note is empty")
	}
	if len(note) > maxNote {
		return nil, invalid("note is too long")
	}
	var view *EventView
	err := s.db.Tx(ctx, func(tx *store.Tx) error {
		v, err := s.loadTask(tx, u, taskID, store.LevelDo)
		if err != nil {
			return err
		}
		if date.IsZero() {
			date = v.Today
		}
		if date.After(v.Today) {
			return invalid("date is in the future")
		}
		ev := &store.Event{
			TaskID: taskID, Kind: store.EventNote, UserID: u.ID, Date: date, Occurrence: v.State.Due,
			SlotID: string(v.State.Slot), Note: note, CreatedAt: s.now(),
		}
		if err := tx.InsertEvent(ev); err != nil {
			return err
		}
		views, err := s.eventViews(tx, []*store.Event{ev})
		view = views[0]
		return err
	})
	return view, err
}

// EventEdit is a change to a history entry. Nil fields are left alone.
type EventEdit struct {
	Date *engine.Date
	Note *string
	// MarkDone turns a missed event into a done event.
	MarkDone bool
}

// EditEvent changes a history entry. Users with do access can edit their own
// and system-recorded events; full access is needed for anyone else's. Editing
// completions of an interval task recomputes its due date.
func (s *Service) EditEvent(ctx context.Context, u *store.User, id string, edit EventEdit) (*EventView, *TaskView, error) {
	var eventView *EventView
	var taskView *TaskView
	err := s.db.Tx(ctx, func(tx *store.Tx) error {
		ev, v, err := s.loadEvent(tx, u, id)
		if err != nil {
			return err
		}
		if edit.Date != nil {
			if edit.Date.IsZero() {
				return invalid("date is required")
			}
			if edit.Date.After(v.Today) {
				return invalid("date is in the future")
			}
			ev.Date = *edit.Date
		}
		if edit.Note != nil {
			note := strings.TrimSpace(*edit.Note)
			if len(note) > maxNote {
				return invalid("note is too long")
			}
			if note == "" && ev.Kind == store.EventNote {
				return invalid("note is empty; delete it instead")
			}
			ev.Note = note
		}
		if edit.MarkDone {
			if ev.Kind != engine.EventMissed {
				return invalid("only missed events can be marked done")
			}
			ev.Kind, ev.UserID = engine.EventDone, u.ID
		}
		ev.EditedAt = s.now()
		if err := tx.UpdateEvent(ev); err != nil {
			return err
		}
		if ev.Kind == engine.EventDone {
			if err := s.recompute(tx, v); err != nil {
				return err
			}
		}
		views, err := s.eventViews(tx, []*store.Event{ev})
		if err != nil {
			return err
		}
		eventView, taskView = views[0], v
		return s.finish(tx, u, v)
	})
	return eventView, taskView, conflict(err)
}

func (s *Service) DeleteEvent(ctx context.Context, u *store.User, id string) (*TaskView, error) {
	var taskView *TaskView
	err := s.db.Tx(ctx, func(tx *store.Tx) error {
		ev, v, err := s.loadEvent(tx, u, id)
		if err != nil {
			return err
		}
		if err := tx.DeleteEvent(id); err != nil {
			return err
		}
		if ev.Kind == engine.EventDone {
			if err := s.recompute(tx, v); err != nil {
				return err
			}
		}
		taskView = v
		return s.finish(tx, u, v)
	})
	return taskView, conflict(err)
}

// loadEvent loads an event and its task, checking that u may edit it.
func (s *Service) loadEvent(tx *store.Tx, u *store.User, id string) (*store.Event, *TaskView, error) {
	ev, err := tx.GetEvent(id)
	if err != nil {
		return nil, nil, ErrNotFound
	}
	min := store.LevelFull
	if ev.UserID == "" || ev.UserID == u.ID {
		min = store.LevelDo
	}
	v, err := s.loadTask(tx, u, ev.TaskID, min)
	if err != nil {
		return nil, nil, err
	}
	return ev, v, nil
}

// recompute resets an interval task's due date from its latest completion.
func (s *Service) recompute(tx *store.Tx, v *TaskView) error {
	last, err := tx.LastDone(v.ID)
	if err != nil {
		return err
	}
	state := v.Def.RecomputeFromHistory(v.State, last)
	if state.Due == v.State.Due {
		return nil
	}
	v.State = state
	v.UpdatedAt = s.now()
	return tx.UpdateTask(v.Task)
}

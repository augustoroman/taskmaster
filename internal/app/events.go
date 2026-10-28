package app

import (
	"context"
	"slices"
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
		reverted := false
		if ev.Kind == engine.EventDone || (ev.Kind == engine.EventSkipped && !ev.Data.Merged) {
			if reverted, err = s.revert(tx, u, v, ev); err != nil {
				return err
			}
		}
		if !reverted && ev.Kind == engine.EventDone {
			if err := s.recompute(tx, v); err != nil {
				return err
			}
		}
		taskView = v
		return s.finish(tx, u, v)
	})
	return taskView, conflict(err)
}

// revert puts a task back to how it was before ev, a completion or skip
// being deleted, if nothing else has happened to the task since: the same
// occurrence is pending again (with its deferral, its checked items and, for
// a cycle, the slot that was done). It reports whether it did.
func (s *Service) revert(tx *store.Tx, u *store.User, v *TaskView, ev *store.Event) (bool, error) {
	// Checked items on the new occurrence aren't events, but mean it has been
	// worked on since.
	if !v.ArchivedAt.IsZero() || v.State.Paused || len(v.State.Checks) > 0 {
		return false, nil
	}
	later, err := tx.HasEventsAfter(v.ID, ev.CreatedAt)
	if err != nil || later {
		return false, err
	}
	occ := ev.Occurrence
	st := engine.State{Due: occ, Slot: v.State.Slot}
	if v.Kind == engine.KindCycle && slices.Contains(v.Def.Slots, engine.SlotID(ev.SlotID)) {
		st.Slot = engine.SlotID(ev.SlotID)
	}
	// Restore the occurrence's deferral, unless it had been cleared.
	d, err := tx.LatestEventOf(v.ID, occ, ev.CreatedAt, engine.EventDeferred, engine.EventDeferralCleared)
	if err != nil {
		return false, err
	}
	if d != nil && d.Kind == engine.EventDeferred && !d.Data.To.IsZero() {
		st.Deferred, st.DeferredFrom, st.Due = true, occ, d.Data.To
	}
	// Restore the checklist items that had been checked.
	v.CheckedBy = nil
	for _, item := range ev.Data.Checked {
		if !slices.Contains(v.Def.Checklist, engine.ItemID(item)) {
			continue
		}
		if st.Checks == nil {
			st.Checks, v.CheckedBy = map[engine.ItemID]engine.Date{}, map[engine.ItemID]string{}
		}
		st.Checks[engine.ItemID(item)] = ev.Date
		v.CheckedBy[engine.ItemID(item)] = ev.UserID
	}
	if err := tx.DeleteMergedSkips(v.ID, ev.CreatedAt); err != nil {
		return false, err
	}
	v.State = st
	syncCheckedBy(v.Task, u.ID)
	v.UpdatedAt = s.now()
	if err := tx.UpdateTask(v.Task); err != nil {
		return false, err
	}
	// The restored date may have passed (e.g. a fixed task now missed).
	if _, err := s.catchup(tx, v.Task); err != nil {
		return false, err
	}
	return true, nil
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

package app

import (
	"context"
	"errors"
	"maps"
	"strings"
	"time"

	"github.com/augustoroman/taskmaster/internal/engine"
	"github.com/augustoroman/taskmaster/internal/store"
)

// ActionError wraps an engine error (e.g. the task is paused) that makes the
// action impossible in the task's current state.
type ActionError struct{ Err error }

func (e *ActionError) Error() string { return e.Err.Error() }
func (e *ActionError) Unwrap() error { return e.Err }

// ActionResult is a task after an action, with the events the action recorded.
type ActionResult struct {
	Task   *TaskView
	Events []*EventView
}

// Action is a request to act on a task's current occurrence.
type Action struct {
	TaskID string
	// Version, if non-zero, must match the task's current version.
	Version int64
	Note    string
}

const maxNote = 20_000

type actOptions struct {
	min store.Level
	// noteOn is the event kind the note is attached to; if the action records
	// no such event, the note becomes its own note event.
	noteOn engine.EventKind
	// noteItem is the checklist item a standalone note is about.
	noteItem string
	apply    func(v *TaskView) (engine.State, []engine.Event, error)
}

func (s *Service) act(ctx context.Context, u *store.User, a Action, opt actOptions) (*ActionResult, error) {
	a.Note = strings.TrimSpace(a.Note)
	if len(a.Note) > maxNote {
		return nil, invalid("note is too long")
	}
	if opt.min == 0 {
		opt.min = store.LevelDo
	}
	var result *ActionResult
	err := s.db.Tx(ctx, func(tx *store.Tx) error {
		v, err := s.loadTask(tx, u, a.TaskID, opt.min)
		if err != nil {
			return err
		}
		if a.Version != 0 && a.Version != v.loadedVersion {
			return ErrConflict
		}
		if !v.ArchivedAt.IsZero() {
			return invalid("task is archived")
		}
		prevState, prevCheckedBy := v.State, maps.Clone(v.CheckedBy)
		state, events, err := opt.apply(v)
		if err != nil {
			return engineError(err)
		}
		now := s.now()

		var stored []*store.Event
		noteAttached := a.Note == ""
		for _, e := range events {
			userID := u.ID
			if e.Kind == engine.EventMissed {
				userID = "" // recorded by the system, not the user
			}
			ev := store.FromEngine(v.ID, userID, e, now)
			if !noteAttached && e.Kind == opt.noteOn {
				ev.Note, noteAttached = a.Note, true
			}
			stored = append(stored, ev)
		}
		if !noteAttached {
			stored = append(stored, &store.Event{
				ID: store.NewID(), TaskID: v.ID, Kind: store.EventNote, UserID: u.ID, Date: v.Today,
				Occurrence: state.Due, SlotID: string(state.Slot), Note: a.Note, CreatedAt: now,
				Data: store.EventData{ItemID: opt.noteItem},
			})
		}

		undo := &store.Undo{Version: v.Version + 1, UserID: u.ID, At: now, State: prevState, CheckedBy: prevCheckedBy}
		for _, ev := range stored {
			undo.EventIDs = append(undo.EventIDs, ev.ID)
		}
		v.State = state
		syncCheckedBy(v.Task, u.ID)
		v.UpdatedAt = now
		v.Undo = undo
		if err := tx.UpdateTask(v.Task); err != nil {
			return err
		}
		for _, ev := range stored {
			if err := tx.InsertEvent(ev); err != nil {
				return err
			}
		}
		views, err := s.eventViews(tx, stored)
		if err != nil {
			return err
		}
		result = &ActionResult{Task: v, Events: views}
		return s.finish(tx, u, v)
	})
	return result, conflict(err)
}

// undoWindow is how long after an action it can be undone.
const undoWindow = 15 * time.Minute

// Undo reverts the latest action on a task: its state goes back to what it
// was and the history entries the action recorded are removed. It only works
// for the person who acted, within undoWindow, and while nothing else has
// changed the task. version, if non-zero, must be the version the action
// produced.
func (s *Service) Undo(ctx context.Context, u *store.User, taskID string, version int64) (*TaskView, error) {
	var view *TaskView
	err := s.db.Tx(ctx, func(tx *store.Tx) error {
		v, err := s.loadTask(tx, u, taskID, store.LevelDo)
		if err != nil {
			return err
		}
		un := v.Undo
		if un == nil || un.Version != v.Version || un.UserID != u.ID || s.now().Sub(un.At) > undoWindow ||
			(version != 0 && version != v.Version) {
			return invalid("that can't be undone anymore")
		}
		for _, id := range un.EventIDs {
			if err := tx.DeleteEvent(id); err != nil {
				return err
			}
		}
		v.State, v.CheckedBy, v.Undo = un.State, un.CheckedBy, nil
		v.UpdatedAt = s.now()
		if err := tx.UpdateTask(v.Task); err != nil {
			return err
		}
		view = v
		return s.finish(tx, u, v)
	})
	return view, conflict(err)
}

func engineError(err error) error {
	var invalidErr *InvalidError
	if errors.As(err, &invalidErr) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrPermission) {
		return err
	}
	return &ActionError{err}
}

// Complete marks the current occurrence done today. asSlot completes a cycle
// as a different slot; force completes despite unchecked checklist items.
func (s *Service) Complete(ctx context.Context, u *store.User, a Action, asSlot string, force bool) (*ActionResult, error) {
	return s.act(ctx, u, a, actOptions{noteOn: engine.EventDone, apply: func(v *TaskView) (engine.State, []engine.Event, error) {
		return v.Def.Complete(v.State, v.Today, engine.CompleteOptions{AsSlot: engine.SlotID(asSlot), Force: force})
	}})
}

func (s *Service) Skip(ctx context.Context, u *store.User, a Action) (*ActionResult, error) {
	return s.act(ctx, u, a, actOptions{noteOn: engine.EventSkipped, apply: func(v *TaskView) (engine.State, []engine.Event, error) {
		return v.Def.Skip(v.State, v.Today)
	}})
}

// CheckItem checks a checklist item; checking the last one completes the task.
func (s *Service) CheckItem(ctx context.Context, u *store.User, a Action, itemID string) (*ActionResult, error) {
	return s.act(ctx, u, a, actOptions{noteOn: engine.EventDone, noteItem: itemID, apply: func(v *TaskView) (engine.State, []engine.Event, error) {
		return v.Def.Check(v.State, v.Today, engine.ItemID(itemID), engine.Date{})
	}})
}

func (s *Service) UncheckItem(ctx context.Context, u *store.User, a Action, itemID string) (*ActionResult, error) {
	return s.act(ctx, u, a, actOptions{noteItem: itemID, apply: func(v *TaskView) (engine.State, []engine.Event, error) {
		return v.Def.Uncheck(v.State, v.Today, engine.ItemID(itemID))
	}})
}

func (s *Service) Defer(ctx context.Context, u *store.User, a Action, to engine.Date) (*ActionResult, error) {
	if to.IsZero() {
		return nil, invalid("a date to defer to is required")
	}
	return s.act(ctx, u, a, actOptions{noteOn: engine.EventDeferred, apply: func(v *TaskView) (engine.State, []engine.Event, error) {
		return v.Def.Defer(v.State, v.Today, to)
	}})
}

func (s *Service) ClearDeferral(ctx context.Context, u *store.User, a Action) (*ActionResult, error) {
	return s.act(ctx, u, a, actOptions{noteOn: engine.EventDeferralCleared, apply: func(v *TaskView) (engine.State, []engine.Event, error) {
		return v.Def.ClearDeferral(v.State, v.Today)
	}})
}

func (s *Service) SetCycleSlot(ctx context.Context, u *store.User, a Action, slotID string) (*ActionResult, error) {
	return s.act(ctx, u, a, actOptions{noteOn: engine.EventSlotSet, apply: func(v *TaskView) (engine.State, []engine.Event, error) {
		return v.Def.SetSlot(v.State, v.Today, engine.SlotID(slotID))
	}})
}

// Pause requires full access. A non-zero until resumes it automatically.
func (s *Service) Pause(ctx context.Context, u *store.User, a Action, until engine.Date) (*ActionResult, error) {
	return s.act(ctx, u, a, actOptions{min: store.LevelFull, noteOn: engine.EventPaused, apply: func(v *TaskView) (engine.State, []engine.Event, error) {
		return v.Def.Pause(v.State, v.Today, until)
	}})
}

func (s *Service) Resume(ctx context.Context, u *store.User, a Action) (*ActionResult, error) {
	return s.act(ctx, u, a, actOptions{min: store.LevelFull, noteOn: engine.EventResumed, apply: func(v *TaskView) (engine.State, []engine.Event, error) {
		return v.Def.Resume(v.State, v.Today)
	}})
}

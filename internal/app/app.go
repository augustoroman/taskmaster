// Package app implements taskmaster's operations on behalf of a user: access
// control, keeping task state current, and recording history. It sits between
// the API handlers and the store, and is where the rules in docs/design.md §4
// and §6 are enforced.
package app

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/augustoroman/taskmaster/internal/engine"
	"github.com/augustoroman/taskmaster/internal/store"
)

var (
	// ErrNotFound is returned for things that don't exist or that the user
	// can't see.
	ErrNotFound = errors.New("not found")
	// ErrPermission is returned when the user can see something but not do
	// what they asked.
	ErrPermission = errors.New("permission denied")
	// ErrConflict means the task changed since the client read it.
	ErrConflict = errors.New("task was changed by someone else; reload and try again")
	// ErrNotInvited means the email has no account and no invitation.
	ErrNotInvited = errors.New("not invited")
)

// InvalidError is a problem with the request's input.
type InvalidError struct{ Msg string }

func (e *InvalidError) Error() string { return e.Msg }

func invalid(format string, args ...any) error {
	return &InvalidError{fmt.Sprintf(format, args...)}
}

type Service struct {
	db     *store.DB
	now    func() time.Time
	admins map[string]bool

	locMu sync.Mutex
	locs  map[string]*time.Location
}

// New returns a service. adminEmails can always log in; now is the clock
// (time.Now in production).
func New(db *store.DB, adminEmails []string, now func() time.Time) *Service {
	admins := map[string]bool{}
	for _, e := range adminEmails {
		if e = store.NormalizeEmail(e); e != "" {
			admins[e] = true
		}
	}
	return &Service{db: db, now: now, admins: admins, locs: map[string]*time.Location{}}
}

// loginRefresh is how often a returning user's last-login time and profile
// are saved.
const loginRefresh = time.Hour

// Login returns the user for a verified email, creating them on first login
// if they are an admin or have been invited. name and picture come from the
// identity provider and refresh the stored profile.
func (s *Service) Login(ctx context.Context, email, name, picture string) (*store.User, error) {
	email = store.NormalizeEmail(email)
	if email == "" {
		return nil, ErrNotInvited
	}
	var user *store.User
	err := s.db.Tx(ctx, func(tx *store.Tx) error {
		now := s.now()
		u, err := tx.GetUserByEmail(email)
		if err == nil {
			user = u
			changed := (name != "" && name != u.Name) || (picture != "" && picture != u.Picture)
			if !changed && now.Sub(u.LastLoginAt) < loginRefresh {
				return nil
			}
			if name != "" {
				u.Name = name
			}
			if picture != "" {
				u.Picture = picture
			}
			u.LastLoginAt = now
			return tx.UpdateUser(u)
		}
		if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		invited, err := tx.HasPendingShare(email)
		if err != nil {
			return err
		}
		if !invited && !s.admins[email] {
			return ErrNotInvited
		}
		user = &store.User{Email: email, Name: name, Picture: picture, CreatedAt: now, LastLoginAt: now}
		if err := tx.InsertUser(user); err != nil {
			return err
		}
		return tx.ClaimShares(email, user.ID)
	})
	return user, err
}

func (s *Service) GetMe(ctx context.Context, u *store.User) (*store.User, error) {
	var me *store.User
	err := s.db.Tx(ctx, func(tx *store.Tx) (err error) {
		me, err = tx.GetUser(u.ID)
		return err
	})
	return me, err
}

// UpdateMe changes the user's name and time zone. Empty values are left alone.
func (s *Service) UpdateMe(ctx context.Context, u *store.User, name, tz string) (*store.User, error) {
	if tz != "" {
		if _, err := s.location(tz); err != nil {
			return nil, err
		}
	}
	var me *store.User
	err := s.db.Tx(ctx, func(tx *store.Tx) (err error) {
		if me, err = tx.GetUser(u.ID); err != nil {
			return err
		}
		if name != "" {
			me.Name = name
		}
		if tz != "" {
			me.TZ = tz
		}
		return tx.UpdateUser(me)
	})
	return me, err
}

func (s *Service) location(tz string) (*time.Location, error) {
	s.locMu.Lock()
	defer s.locMu.Unlock()
	if loc, ok := s.locs[tz]; ok {
		return loc, nil
	}
	loc, err := time.LoadLocation(tz)
	if err != nil || tz == "" || tz == "Local" {
		return nil, invalid("unknown time zone %q", tz)
	}
	s.locs[tz] = loc
	return loc, nil
}

// today is the current date in tz (UTC if tz is invalid).
func (s *Service) today(tz string) engine.Date {
	loc, err := s.location(tz)
	if err != nil {
		loc = time.UTC
	}
	return engine.Today(s.now(), loc)
}

// TaskView is a task as one user sees it, brought up to date.
type TaskView struct {
	*store.Task
	Level store.Level
	Def   engine.Task
	Today engine.Date
	// VisibleTagIDs are the task's tags that the user can see.
	VisibleTagIDs []string
	// Users has the creator and anyone who checked a checklist item.
	Users map[string]*store.User
	// LastDone is the date of the latest completion, if any.
	LastDone engine.Date
	// DueEditable means the due date can still be set directly (see
	// dueEditable).
	DueEditable bool
	// loadedVersion is the version before any catch-up was saved, for
	// comparing with the version the client sent.
	loadedVersion int64
}

type EventView struct {
	*store.Event
	User *store.User // nil for system events
}

// loadTask loads a task that u can access at level min or above and brings it
// up to date.
func (s *Service) loadTask(tx *store.Tx, u *store.User, id string, min store.Level) (*TaskView, error) {
	return s.loadTaskAt(tx, u, id, min, engine.Date{})
}

// loadTaskAt is loadTask, but brings the task up to date only as of day (if
// set), which may be in the past: for replaying actions done offline.
func (s *Service) loadTaskAt(tx *store.Tx, u *store.User, id string, min store.Level, day engine.Date) (*TaskView, error) {
	level, err := tx.TaskLevel(u.ID, id)
	if err != nil {
		return nil, err
	}
	if level == store.LevelNone {
		return nil, ErrNotFound
	}
	if level < min {
		return nil, ErrPermission
	}
	t, err := tx.GetTask(id)
	if err != nil {
		return nil, err
	}
	v, err := s.catchupAt(tx, t, day)
	if err != nil {
		return nil, err
	}
	v.Level = level
	return v, nil
}

// catchup runs the engine's catch-up (misses, automatic resumes) and saves
// the result if anything changed. Archived tasks are left as they are.
func (s *Service) catchup(tx *store.Tx, t *store.Task) (*TaskView, error) {
	return s.catchupAt(tx, t, engine.Date{})
}

// catchupAt is catchup as of day (default today); the view's Today is day.
func (s *Service) catchupAt(tx *store.Tx, t *store.Task, day engine.Date) (*TaskView, error) {
	def, err := t.Engine()
	if err != nil {
		return nil, fmt.Errorf("task %s has an invalid schedule: %w", t.ID, err)
	}
	if day.IsZero() {
		day = s.today(t.TZ)
	}
	v := &TaskView{Task: t, Def: def, Today: day, loadedVersion: t.Version}
	if !t.ArchivedAt.IsZero() {
		return v, nil
	}
	state, events := def.Catchup(t.State, v.Today)
	if len(events) == 0 && reflect.DeepEqual(state, t.State) {
		return v, nil
	}
	now := s.now()
	t.State = state
	syncCheckedBy(t, "")
	t.UpdatedAt = now
	if err := tx.UpdateTask(t); err != nil {
		return nil, err
	}
	for _, e := range events {
		if err := tx.InsertEvent(store.FromEngine(t.ID, "", e, now)); err != nil {
			return nil, err
		}
	}
	return v, nil
}

// syncCheckedBy drops CheckedBy entries for items no longer checked and
// attributes newly checked items to userID.
func syncCheckedBy(t *store.Task, userID string) {
	for item := range t.CheckedBy {
		if _, ok := t.State.Checks[item]; !ok {
			delete(t.CheckedBy, item)
		}
	}
	for item := range t.State.Checks {
		if _, ok := t.CheckedBy[item]; !ok {
			if t.CheckedBy == nil {
				t.CheckedBy = map[engine.ItemID]string{}
			}
			t.CheckedBy[item] = userID
		}
	}
}

// finish fills in the per-user parts of task views: visible tags and users.
func (s *Service) finish(tx *store.Tx, u *store.User, views ...*TaskView) error {
	tags, err := tx.UserTags(u.ID)
	if err != nil {
		return err
	}
	visible := map[string]bool{}
	for _, t := range tags {
		visible[t.ID] = true
	}
	var userIDs, taskIDs []string
	for _, v := range views {
		taskIDs = append(taskIDs, v.ID)
		v.VisibleTagIDs = nil
		for _, id := range v.TagIDs {
			if visible[id] {
				v.VisibleTagIDs = append(v.VisibleTagIDs, id)
			}
		}
		userIDs = append(userIDs, v.CreatorID)
		for _, id := range v.CheckedBy {
			userIDs = append(userIDs, id)
		}
	}
	users, err := tx.GetUsers(userIDs)
	if err != nil {
		return err
	}
	lastDone, err := tx.LastDoneDates(taskIDs)
	if err != nil {
		return err
	}
	history, err := tx.TasksWithHistory(taskIDs)
	if err != nil {
		return err
	}
	for _, v := range views {
		v.Users = users
		v.LastDone = lastDone[v.ID]
		v.DueEditable = dueEditable(v.Task, history[v.ID]) == nil
	}
	return nil
}

func (s *Service) eventViews(tx *store.Tx, events []*store.Event) ([]*EventView, error) {
	var ids []string
	for _, e := range events {
		if e.UserID != "" {
			ids = append(ids, e.UserID)
		}
	}
	users, err := tx.GetUsers(ids)
	if err != nil {
		return nil, err
	}
	out := make([]*EventView, len(events))
	for i, e := range events {
		out[i] = &EventView{Event: e, User: users[e.UserID]}
	}
	return out, nil
}

// Sweep brings every active task up to date: it records missed fixed
// occurrences and resumes tasks whose pause has ended. It returns how many
// tasks changed.
func (s *Service) Sweep(ctx context.Context) (int, error) {
	var ids []string
	err := s.db.Tx(ctx, func(tx *store.Tx) (err error) {
		ids, err = tx.ActiveTaskIDs()
		return err
	})
	if err != nil {
		return 0, err
	}
	changed := 0
	var errs []error
	for _, id := range ids {
		if ctx.Err() != nil {
			return changed, ctx.Err()
		}
		err := s.db.Tx(ctx, func(tx *store.Tx) error {
			t, err := tx.GetTask(id)
			if err != nil {
				return err
			}
			before := t.Version
			if _, err := s.catchup(tx, t); err != nil {
				return err
			}
			if t.Version != before {
				changed++
			}
			return nil
		})
		// Keep going: one broken task shouldn't stall the rest.
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			errs = append(errs, fmt.Errorf("sweeping task %s: %w", id, err))
		}
	}
	return changed, errors.Join(errs...)
}

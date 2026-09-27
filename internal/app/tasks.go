package app

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/augustoroman/taskmaster/internal/engine"
	"github.com/augustoroman/taskmaster/internal/store"
)

// TaskInput is the editable part of a task.
type TaskInput struct {
	Title       string
	Description string
	Priority    engine.Priority
	LeadDays    int
	TZ          string // default: the user's time zone
	Kind        engine.Kind
	Interval    engine.Interval
	RRule       string
	RRuleStart  engine.Date // default: today
	// CarryOver (fixed tasks) keeps a missed date pending until it's done.
	CarryOver bool
	// Slots and Checklist entries with an empty ID are new.
	Slots     []store.Slot
	Checklist []store.ChecklistItem
}

const (
	maxTitle       = 200
	maxDescription = 100_000
)

func (in *TaskInput) normalize(s *Service, u *store.User) error {
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" {
		return invalid("title is required")
	}
	if len(in.Title) > maxTitle {
		return invalid("title is too long")
	}
	if len(in.Description) > maxDescription {
		return invalid("description is too long")
	}
	if in.Priority == 0 {
		in.Priority = engine.Normal
	}
	if in.TZ == "" {
		in.TZ = u.TZ
	}
	if in.TZ == "" {
		in.TZ = "UTC"
	}
	if _, err := s.location(in.TZ); err != nil {
		return err
	}
	if in.Kind == engine.KindFixed || in.Kind == engine.KindCycle {
		if in.RRuleStart.IsZero() {
			in.RRuleStart = s.today(in.TZ)
		}
	} else {
		in.RRule, in.RRuleStart = "", engine.Date{}
	}
	if in.Kind != engine.KindInterval {
		in.Interval = engine.Interval{}
	}
	if in.Kind != engine.KindFixed {
		in.CarryOver = false
	}
	for i := range in.Slots {
		if in.Slots[i].Title = strings.TrimSpace(in.Slots[i].Title); in.Slots[i].Title == "" {
			return invalid("cycle slots need a title")
		}
	}
	for i := range in.Checklist {
		if in.Checklist[i].Title = strings.TrimSpace(in.Checklist[i].Title); in.Checklist[i].Title == "" {
			return invalid("checklist items need a title")
		}
	}
	return nil
}

func (in *TaskInput) scheduleOf() (engine.Kind, engine.Interval, string, engine.Date) {
	return in.Kind, in.Interval, in.RRule, in.RRuleStart
}

// definition checks that t's schedule is valid.
func definition(t *store.Task) (engine.Task, error) {
	def, err := t.Engine()
	if err != nil {
		return def, invalid("%v", err)
	}
	return def, nil
}

func (s *Service) CreateTask(ctx context.Context, u *store.User, in TaskInput, tagIDs []string, firstDue engine.Date) (*TaskView, error) {
	if err := in.normalize(s, u); err != nil {
		return nil, err
	}
	var view *TaskView
	err := s.db.Tx(ctx, func(tx *store.Tx) error {
		for _, tag := range tagIDs {
			if err := s.requireTagLevel(tx, u, tag, store.LevelFull); err != nil {
				return err
			}
		}
		now := s.now()
		t := &store.Task{
			CreatorID: u.ID,
			CreatedAt: now,
			UpdatedAt: now,
			TagIDs:    dedupe(tagIDs),
		}
		if err := applyInput(t, in); err != nil {
			return err
		}
		def, err := definition(t)
		if err != nil {
			return err
		}
		t.State = def.Init(s.today(t.TZ), firstDue)
		if err := tx.InsertTask(t); err != nil {
			return err
		}
		view = &TaskView{Task: t, Level: store.LevelFull, Def: def, Today: s.today(t.TZ), loadedVersion: t.Version}
		return s.finish(tx, u, view)
	})
	return view, err
}

// applyInput copies in's fields onto t, merging slots and checklist items by
// ID: new entries get IDs, and existing ones left out are marked removed.
func applyInput(t *store.Task, in TaskInput) error {
	t.Title, t.Description, t.Priority, t.LeadDays, t.TZ = in.Title, in.Description, in.Priority, in.LeadDays, in.TZ
	t.Kind, t.Interval, t.RRule, t.RRuleStart = in.scheduleOf()
	t.CarryOver = in.CarryOver

	oldSlots := map[string]store.Slot{}
	for _, s := range t.Slots {
		oldSlots[s.ID] = s
	}
	var slots []store.Slot
	for _, s := range in.Slots {
		if s.ID == "" {
			s.ID = store.NewID()
		} else if _, ok := oldSlots[s.ID]; !ok {
			return invalid("unknown cycle slot %q", s.ID)
		}
		delete(oldSlots, s.ID)
		s.Removed = false
		slots = append(slots, s)
	}
	for _, s := range t.Slots {
		if _, left := oldSlots[s.ID]; left {
			s.Removed = true
			slots = append(slots, s)
		}
	}
	t.Slots = slots

	oldItems := map[string]store.ChecklistItem{}
	for _, item := range t.Checklist {
		oldItems[item.ID] = item
	}
	var items []store.ChecklistItem
	for _, item := range in.Checklist {
		if item.ID == "" {
			item.ID = store.NewID()
		} else if _, ok := oldItems[item.ID]; !ok {
			return invalid("unknown checklist item %q", item.ID)
		}
		delete(oldItems, item.ID)
		item.Removed = false
		items = append(items, item)
	}
	for _, item := range t.Checklist {
		if _, left := oldItems[item.ID]; left {
			item.Removed = true
			items = append(items, item)
			delete(t.State.Checks, engine.ItemID(item.ID))
		}
	}
	t.Checklist = items
	return nil
}

// UpdateTask replaces a task's editable fields. If the schedule changes, the
// current occurrence is recomputed and a schedule_changed event is recorded.
// If tagIDs is non-nil, the task's tags that u can see become tagIDs (tags u
// can't see are kept); adding a tag requires full access to it. A non-zero due
// sets the due date directly, which is only allowed while the task has no
// history (see dueEditable).
func (s *Service) UpdateTask(ctx context.Context, u *store.User, id string, version int64, in TaskInput, tagIDs []string, due engine.Date) (*TaskView, error) {
	if err := in.normalize(s, u); err != nil {
		return nil, err
	}
	var view *TaskView
	err := s.db.Tx(ctx, func(tx *store.Tx) error {
		v, err := s.loadTask(tx, u, id, store.LevelFull)
		if err != nil {
			return err
		}
		if version != v.loadedVersion {
			return ErrConflict
		}
		t := v.Task
		if !due.IsZero() && due != t.State.Due {
			history, err := tx.TasksWithHistory([]string{t.ID})
			if err != nil {
				return err
			}
			if err := dueEditable(t, history[t.ID]); err != nil {
				return err
			}
			if in.Kind != engine.KindInterval && in.Kind != engine.KindOnce {
				return invalid("tasks on set dates follow their rule; change its start date instead")
			}
		} else {
			due = engine.Date{}
		}
		if tagIDs != nil {
			if err := s.setTags(tx, u, t, tagIDs); err != nil {
				return err
			}
		}
		oldKind, oldInterval, oldRule, oldStart := t.Kind, t.Interval, t.RRule, t.RRuleStart
		oldDue := t.State.Due
		if err := applyInput(t, in); err != nil {
			return err
		}
		def, err := definition(t)
		if err != nil {
			return err
		}
		today := s.today(t.TZ)
		now := s.now()
		scheduleChanged := t.Kind != oldKind || t.Interval != oldInterval || t.RRule != oldRule || t.RRuleStart != oldStart
		if scheduleChanged {
			first := oldDue
			if !due.IsZero() {
				first = due
			}
			if t.Kind == engine.KindInterval && due.IsZero() {
				last, err := tx.LastDone(t.ID)
				if err != nil {
					return err
				}
				if !last.IsZero() {
					first = t.Interval.After(last)
				}
			}
			state := def.Init(today, first)
			state.Paused, state.PauseUntil = t.State.Paused, t.State.PauseUntil
			if t.Kind == engine.KindCycle && slices.Contains(def.Slots, t.State.Slot) {
				state.Slot = t.State.Slot
			}
			t.State = state
			err := tx.InsertEvent(&store.Event{
				TaskID: t.ID, Kind: store.EventScheduleChanged, UserID: u.ID, Date: today, CreatedAt: now,
				Data: store.EventData{From: oldDue, To: state.Due},
			})
			if err != nil {
				return err
			}
		} else if !due.IsZero() {
			t.State.Due = due
		} else if t.Kind == engine.KindCycle && !slices.Contains(def.Slots, t.State.Slot) {
			// The current slot was removed.
			t.State.Slot = def.Slots[0]
		}
		syncCheckedBy(t, u.ID)
		t.UpdatedAt = now
		if err := tx.UpdateTask(t); err != nil {
			return err
		}
		v.Def, v.Today = def, today
		view = v
		return s.finish(tx, u, v)
	})
	return view, conflict(err)
}

func (s *Service) GetTask(ctx context.Context, u *store.User, id string) (*TaskView, error) {
	var view *TaskView
	err := s.db.Tx(ctx, func(tx *store.Tx) (err error) {
		if view, err = s.loadTask(tx, u, id, store.LevelRead); err != nil {
			return err
		}
		return s.finish(tx, u, view)
	})
	return view, err
}

type TaskFilter struct {
	TagIDs          []string // any of these
	IncludeArchived bool
	IncludeDone     bool
	IncludeHidden   bool
	UpdatedSince    time.Time
}

// ListTasks returns the tasks u can see, soonest due first.
func (s *Service) ListTasks(ctx context.Context, u *store.User, f TaskFilter) ([]*TaskView, error) {
	var views []*TaskView
	err := s.db.Tx(ctx, func(tx *store.Tx) error {
		levels, err := tx.VisibleTasks(u.ID)
		if err != nil {
			return err
		}
		ids := make([]string, 0, len(levels))
		for id := range levels {
			ids = append(ids, id)
		}
		tasks, err := tx.GetTasks(ids)
		if err != nil {
			return err
		}
		tags, err := tx.UserTags(u.ID)
		if err != nil {
			return err
		}
		hidden := map[string]bool{}
		for _, t := range tags {
			hidden[t.ID] = t.Hidden
		}
		for _, t := range tasks {
			v, err := s.catchup(tx, t)
			if err != nil {
				return err
			}
			v.Level = levels[t.ID]
			if (!f.IncludeArchived && !t.ArchivedAt.IsZero()) ||
				(!f.IncludeDone && t.State.Done) ||
				(!f.UpdatedSince.IsZero() && !t.UpdatedAt.After(f.UpdatedSince)) ||
				(len(f.TagIDs) > 0 && !overlaps(t.TagIDs, f.TagIDs)) ||
				(!f.IncludeHidden && isHidden(t, hidden)) {
				continue
			}
			views = append(views, v)
		}
		slices.SortFunc(views, func(a, b *TaskView) int {
			ad, bd := a.State.Due, b.State.Due
			if ad.IsZero() != bd.IsZero() {
				if ad.IsZero() {
					return 1
				}
				return -1
			}
			return cmp.Or(ad.Compare(bd), strings.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title)), strings.Compare(a.ID, b.ID))
		})
		return s.finish(tx, u, views...)
	})
	return views, err
}

// dueEditable reports why t's due date can't be set directly, or nil if it
// can: only for interval and once tasks that haven't been done, missed,
// skipped, deferred or paused yet. After that, moving the date is a deferral,
// which is recorded.
func dueEditable(t *store.Task, hasHistory bool) error {
	switch {
	case t.Kind != engine.KindInterval && t.Kind != engine.KindOnce:
		return invalid("tasks on set dates follow their rule; change its start date instead")
	case hasHistory || t.State.Deferred || t.State.Paused || t.State.Done:
		return invalid("this task already has history; use Defer to move its due date")
	}
	return nil
}

// setTags makes the tags of t that u can see equal to want.
func (s *Service) setTags(tx *store.Tx, u *store.User, t *store.Task, want []string) error {
	tags, err := tx.UserTags(u.ID)
	if err != nil {
		return err
	}
	visible := map[string]bool{}
	for _, tag := range tags {
		visible[tag.ID] = true
	}
	var next []string
	for _, id := range t.TagIDs {
		if !visible[id] {
			next = append(next, id) // not ours to change
		}
	}
	for _, id := range dedupe(want) {
		if !slices.Contains(t.TagIDs, id) {
			if err := s.requireTagLevel(tx, u, id, store.LevelFull); err != nil {
				return err
			}
		} else if !visible[id] {
			continue // already kept above
		}
		next = append(next, id)
	}
	t.TagIDs = next
	return nil
}

// isHidden reports whether every tag of t that the user can see is hidden.
// hidden has an entry for each tag the user can see.
func isHidden(t *store.Task, hidden map[string]bool) bool {
	seen := false
	for _, id := range t.TagIDs {
		h, visible := hidden[id]
		if !visible {
			continue
		}
		if !h {
			return false
		}
		seen = true
	}
	return seen
}

func overlaps(a, b []string) bool {
	for _, x := range a {
		if slices.Contains(b, x) {
			return true
		}
	}
	return false
}

func dedupe(list []string) []string {
	var out []string
	for _, s := range list {
		if s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// UpcomingItem is a task in the Upcoming view.
type UpcomingItem struct {
	Task    *TaskView
	Urgency engine.Urgency
}

var groupOrder = map[engine.Group]int{engine.GroupOverdue: 0, engine.GroupToday: 1, engine.GroupSoon: 2}

// Upcoming returns the tasks within their lead time, overdue first, then by
// score.
func (s *Service) Upcoming(ctx context.Context, u *store.User, tagIDs []string, includeHidden bool) ([]UpcomingItem, error) {
	tasks, err := s.ListTasks(ctx, u, TaskFilter{TagIDs: tagIDs, IncludeHidden: includeHidden})
	if err != nil {
		return nil, err
	}
	var items []UpcomingItem
	for _, v := range tasks {
		if urgency, ok := v.Def.Rank(v.State, v.Today); ok {
			items = append(items, UpcomingItem{v, urgency})
		}
	}
	slices.SortStableFunc(items, func(a, b UpcomingItem) int {
		return cmp.Or(
			cmp.Compare(groupOrder[a.Urgency.Group], groupOrder[b.Urgency.Group]),
			cmp.Compare(b.Urgency.Score, a.Urgency.Score),
		)
	})
	return items, nil
}

func (s *Service) ArchiveTask(ctx context.Context, u *store.User, id string) (*TaskView, error) {
	return s.editTask(ctx, u, id, func(tx *store.Tx, v *TaskView) error {
		if v.ArchivedAt.IsZero() {
			v.ArchivedAt = s.now()
		}
		return nil
	})
}

// UnarchiveTask restores a task. A fixed task skips the dates it passed while
// archived rather than recording them as missed.
func (s *Service) UnarchiveTask(ctx context.Context, u *store.User, id string) (*TaskView, error) {
	return s.editTask(ctx, u, id, func(tx *store.Tx, v *TaskView) error {
		if !v.ArchivedAt.IsZero() {
			v.ArchivedAt = time.Time{}
			v.State = v.Def.Reactivate(v.State, v.Today)
			syncCheckedBy(v.Task, u.ID)
		}
		return nil
	})
}

func (s *Service) DeleteTask(ctx context.Context, u *store.User, id string) error {
	return s.db.Tx(ctx, func(tx *store.Tx) error {
		if _, err := s.loadTask(tx, u, id, store.LevelFull); err != nil {
			return err
		}
		return tx.DeleteTask(id)
	})
}

// AddTaskTag requires full access to both the task and the tag.
func (s *Service) AddTaskTag(ctx context.Context, u *store.User, taskID, tagID string) (*TaskView, error) {
	return s.editTask(ctx, u, taskID, func(tx *store.Tx, v *TaskView) error {
		if err := s.requireTagLevel(tx, u, tagID, store.LevelFull); err != nil {
			return err
		}
		v.TagIDs = dedupe(append(v.TagIDs, tagID))
		return nil
	})
}

func (s *Service) RemoveTaskTag(ctx context.Context, u *store.User, taskID, tagID string) (*TaskView, error) {
	return s.editTask(ctx, u, taskID, func(tx *store.Tx, v *TaskView) error {
		v.TagIDs = slices.DeleteFunc(v.TagIDs, func(id string) bool { return id == tagID })
		return nil
	})
}

// editTask loads a task with full access, applies fn, and saves it.
func (s *Service) editTask(ctx context.Context, u *store.User, id string, fn func(*store.Tx, *TaskView) error) (*TaskView, error) {
	var view *TaskView
	err := s.db.Tx(ctx, func(tx *store.Tx) error {
		v, err := s.loadTask(tx, u, id, store.LevelFull)
		if err != nil {
			return err
		}
		if err := fn(tx, v); err != nil {
			return err
		}
		v.UpdatedAt = s.now()
		if err := tx.UpdateTask(v.Task); err != nil {
			return err
		}
		view = v
		return s.finish(tx, u, v)
	})
	return view, conflict(err)
}

func conflict(err error) error {
	if errors.Is(err, store.ErrConflict) {
		return ErrConflict
	}
	return err
}

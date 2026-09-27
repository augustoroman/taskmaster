package engine

import (
	"errors"
	"fmt"
	"maps"
	"slices"
)

type (
	SlotID string
	ItemID string
)

// Priority is 1 (high) to 3 (low). More levels may be added later.
type Priority int

const (
	High   Priority = 1
	Normal Priority = 2
	Low    Priority = 3
)

// Task is the scheduling-relevant definition of a task.
type Task struct {
	Kind       Kind
	Interval   Interval    // KindInterval
	Recurrence *Recurrence // KindFixed, KindCycle
	// CarryOver (KindFixed only) keeps a missed date pending until it's done,
	// like a cycle, instead of recording a miss and moving on.
	CarryOver bool
	Slots     []SlotID // KindCycle: the rotation, in order
	Checklist []ItemID // any kind but KindCycle
	Priority  Priority
	Lead      int // lead time in days; 0 means the default (see LeadDays)
}

func (t Task) Validate() error {
	switch t.Kind {
	case KindInterval:
		if err := t.Interval.Validate(); err != nil {
			return err
		}
	case KindFixed, KindCycle:
		if t.Recurrence == nil {
			return fmt.Errorf("%s tasks need a recurrence rule", t.Kind)
		}
	case KindOnce:
	default:
		return fmt.Errorf("unknown schedule kind %q", t.Kind)
	}
	if t.CarryOver && t.Kind != KindFixed {
		return fmt.Errorf("only tasks on set dates can carry over misses")
	}
	if t.Kind == KindCycle {
		if len(t.Slots) == 0 {
			return errors.New("cycle tasks need at least one slot")
		}
		if len(t.Checklist) > 0 {
			return errors.New("cycle tasks can't have checklists")
		}
		if hasDuplicates(t.Slots) {
			return errors.New("duplicate cycle slot")
		}
	} else if len(t.Slots) > 0 {
		return fmt.Errorf("%s tasks can't have cycle slots", t.Kind)
	}
	if hasDuplicates(t.Checklist) {
		return errors.New("duplicate checklist item")
	}
	if t.Priority < High || t.Priority > Low {
		return fmt.Errorf("priority must be %d–%d, got %d", High, Low, t.Priority)
	}
	if t.Lead < 0 {
		return errors.New("lead time can't be negative")
	}
	return nil
}

func hasDuplicates[T comparable](list []T) bool {
	seen := map[T]bool{}
	for _, v := range list {
		if seen[v] {
			return true
		}
		seen[v] = true
	}
	return false
}

// State is a task's current, pending occurrence.
type State struct {
	// Due is the current occurrence's due date. It is zero only for a once
	// task without a due date.
	Due Date `json:"due"`
	// Deferred is set when Due was moved by hand; DeferredFrom is the date the
	// schedule gave before that.
	Deferred     bool `json:"deferred,omitempty"`
	DeferredFrom Date `json:"deferred_from"`
	Paused       bool `json:"paused,omitempty"`
	PauseUntil   Date `json:"pause_until"` // optional automatic resume
	// Slot is a cycle's current slot.
	Slot SlotID `json:"slot,omitempty"`
	// Done is set when a once task is completed or a recurrence has ended.
	Done bool `json:"done,omitempty"`
	// Checks holds the checklist items checked for the current occurrence and
	// the date each was checked.
	Checks map[ItemID]Date `json:"checks,omitempty"`
}

// Occurrence is the schedule-given due date of the current occurrence,
// ignoring any deferral. It identifies the occurrence in events.
func (s State) Occurrence() Date {
	if s.Deferred {
		return s.DeferredFrom
	}
	return s.Due
}

func (s State) clone() State {
	s.Checks = maps.Clone(s.Checks)
	return s
}

type EventKind string

const (
	EventDone            EventKind = "done"
	EventMissed          EventKind = "missed"
	EventSkipped         EventKind = "skipped"
	EventDeferred        EventKind = "deferred"
	EventDeferralCleared EventKind = "deferral_cleared"
	EventPaused          EventKind = "paused"
	EventResumed         EventKind = "resumed"
	EventSlotSet         EventKind = "slot_set"
)

// Event is a history entry produced by the engine. Callers add who did it,
// notes and timestamps.
type Event struct {
	Kind EventKind
	// Date is the civil date the event counts for (e.g. the completion date).
	Date Date
	// Occurrence is the schedule-given due date of the occurrence it refers to.
	Occurrence Date
	Slot       SlotID
	// Merged marks a skipped fixed occurrence that was absorbed by a deferral.
	Merged bool
	// Checked and Unchecked split the checklist for done and missed events.
	Checked   []ItemID
	Unchecked []ItemID
	// From and To are the old and new due dates of a deferral, and To is the
	// resume date of a pause.
	From, To Date
}

var (
	ErrPaused              = errors.New("task is paused")
	ErrNotPaused           = errors.New("task is not paused")
	ErrDone                = errors.New("task is already done")
	ErrFutureDate          = errors.New("date is in the future")
	ErrPastDate            = errors.New("date is in the past")
	ErrNotDeferred         = errors.New("task is not deferred")
	ErrNotCycle            = errors.New("task is not a cycle")
	ErrUnknownSlot         = errors.New("unknown cycle slot")
	ErrUnknownItem         = errors.New("unknown checklist item")
	ErrChecklistIncomplete = errors.New("checklist is incomplete")
)

// Init returns the initial state of a new task. first is the first due date
// for interval tasks (default today) and the optional due date for once tasks;
// fixed and cycle tasks start at the first rule date on or after today.
func (t Task) Init(today, first Date) State {
	var s State
	switch t.Kind {
	case KindInterval:
		s.Due = first
		if s.Due.IsZero() {
			s.Due = today
		}
	case KindOnce:
		s.Due = first
	case KindFixed, KindCycle:
		s.Due = t.Recurrence.OnOrAfter(today)
		s.Done = s.Due.IsZero()
		if t.Kind == KindCycle {
			s.Slot = t.Slots[0]
		}
	}
	return s
}

// Catchup brings s up to date as of today: it resumes a task whose pause has
// ended and records missed fixed occurrences. Every action calls it first;
// callers also run it on read and from the periodic sweep. It is idempotent.
func (t Task) Catchup(s State, today Date) (State, []Event) {
	s = s.clone()
	var events []Event
	if s.Done {
		return s, nil
	}
	if s.Paused && !s.PauseUntil.IsZero() && !s.PauseUntil.After(today) {
		var ev Event
		s, ev = t.resume(s, s.PauseUntil)
		events = append(events, ev)
	}
	if s.Paused || s.Done || !t.skipsMisses() || !s.Due.Before(today) {
		return s, events
	}

	occ := s.Occurrence()
	checked, unchecked := t.splitChecklist(s)
	events = append(events, Event{Kind: EventMissed, Date: s.Due, Occurrence: occ, Checked: checked, Unchecked: unchecked})
	events = append(events, t.mergedSkips(s)...)
	for _, d := range t.Recurrence.Between(s.Due, today.AddDays(-1)) {
		events = append(events, Event{Kind: EventMissed, Date: d, Occurrence: d})
	}
	s = t.newOccurrence(s, t.Recurrence.OnOrAfter(today))
	return s, events
}

// skipsMisses reports whether passed dates are recorded as missed and
// skipped (fixed tasks), rather than carried over until done (cycles and
// carry-over fixed tasks).
func (t Task) skipsMisses() bool { return t.Kind == KindFixed && !t.CarryOver }

// mergedSkips records the fixed occurrences that a deferral absorbed: rule
// dates after the deferred occurrence, up to and including the deferred date.
func (t Task) mergedSkips(s State) []Event {
	if !s.Deferred || !t.skipsMisses() {
		return nil
	}
	var events []Event
	for _, d := range t.Recurrence.Between(s.DeferredFrom, s.Due) {
		events = append(events, Event{Kind: EventSkipped, Date: d, Occurrence: d, Merged: true})
	}
	return events
}

// newOccurrence starts a fresh occurrence due on due; a zero due means the
// recurrence has ended.
func (t Task) newOccurrence(s State, due Date) State {
	s.Due = due
	s.Deferred, s.DeferredFrom = false, Date{}
	s.Checks = nil
	if due.IsZero() && t.Kind != KindOnce {
		s.Done = true
	}
	return s
}

func (t Task) splitChecklist(s State) (checked, unchecked []ItemID) {
	for _, item := range t.Checklist {
		if _, ok := s.Checks[item]; ok {
			checked = append(checked, item)
		} else {
			unchecked = append(unchecked, item)
		}
	}
	return checked, unchecked
}

// actionable runs Catchup and checks that the task can be acted on.
func (t Task) actionable(s State, today Date) (State, []Event, error) {
	s, events := t.Catchup(s, today)
	if s.Done {
		return s, events, ErrDone
	}
	if s.Paused {
		return s, events, ErrPaused
	}
	return s, events, nil
}

type CompleteOptions struct {
	// Date is the completion date; zero means today.
	Date Date
	// AsSlot completes a cycle as this slot instead of the current one; the
	// rotation then continues after it.
	AsSlot SlotID
	// Force completes even if checklist items are unchecked; they are
	// recorded as not done.
	Force bool
}

// Complete marks the current occurrence done.
func (t Task) Complete(s State, today Date, opt CompleteOptions) (State, []Event, error) {
	s, events, err := t.actionable(s, today)
	if err != nil {
		return s, events, err
	}
	c := opt.Date
	if c.IsZero() {
		c = today
	}
	if c.After(today) {
		return s, events, ErrFutureDate
	}
	slot := s.Slot
	if opt.AsSlot != "" {
		if t.Kind != KindCycle {
			return s, events, ErrNotCycle
		}
		if !slices.Contains(t.Slots, opt.AsSlot) {
			return s, events, ErrUnknownSlot
		}
		slot = opt.AsSlot
	}
	checked, unchecked := t.splitChecklist(s)
	if len(unchecked) > 0 && !opt.Force {
		return s, events, ErrChecklistIncomplete
	}
	events = append(events, Event{Kind: EventDone, Date: c, Occurrence: s.Occurrence(), Slot: slot, Checked: checked, Unchecked: unchecked})
	events = append(events, t.mergedSkips(s)...)
	return t.advance(s, c, slot), events, nil
}

// Skip records that the current occurrence won't be done and moves on as if
// it had been completed today. A cycle moves to its next slot.
func (t Task) Skip(s State, today Date) (State, []Event, error) {
	s, events, err := t.actionable(s, today)
	if err != nil {
		return s, events, err
	}
	events = append(events, Event{Kind: EventSkipped, Date: today, Occurrence: s.Occurrence(), Slot: s.Slot})
	events = append(events, t.mergedSkips(s)...)
	return t.advance(s, today, s.Slot), events, nil
}

// advance moves past the current occurrence, which was finished on c; for a
// cycle, slot is the slot that was done.
func (t Task) advance(s State, c Date, slot SlotID) State {
	switch t.Kind {
	case KindInterval:
		return t.newOccurrence(s, t.Interval.After(c))
	case KindOnce:
		s = t.newOccurrence(s, s.Due)
		s.Done = true
		return s
	case KindFixed:
		if t.CarryOver {
			// Like a cycle: done late, the next date is the first after it was done.
			return t.newOccurrence(s, t.Recurrence.After(MaxDate(c, s.Due)))
		}
		// Doing it early doesn't move later dates. s.Due is the deferred date if
		// the occurrence was deferred, so later rule dates up to it are merged.
		return t.newOccurrence(s, t.Recurrence.After(s.Due))
	case KindCycle:
		s = t.newOccurrence(s, t.Recurrence.After(MaxDate(c, s.Due)))
		s.Slot = t.Slots[(slices.Index(t.Slots, slot)+1)%len(t.Slots)]
		return s
	}
	panic(fmt.Sprintf("unknown schedule kind %q", t.Kind))
}

// Defer moves the current occurrence's due date to `to` without changing the
// schedule. For fixed tasks, rule dates up to `to` merge into this occurrence.
func (t Task) Defer(s State, today, to Date) (State, []Event, error) {
	s, events, err := t.actionable(s, today)
	if err != nil {
		return s, events, err
	}
	if to.Before(today) {
		return s, events, ErrPastDate
	}
	ev := Event{Kind: EventDeferred, Date: today, Occurrence: s.Occurrence(), Slot: s.Slot, From: s.Due, To: to}
	if !s.Deferred {
		s.Deferred, s.DeferredFrom = true, s.Due
	}
	s.Due = to
	if s.Due == s.DeferredFrom {
		s.Deferred, s.DeferredFrom = false, Date{}
	}
	return s, append(events, ev), nil
}

// ClearDeferral puts back the due date the schedule gave.
func (t Task) ClearDeferral(s State, today Date) (State, []Event, error) {
	s, events, err := t.actionable(s, today)
	if err != nil {
		return s, events, err
	}
	if !s.Deferred {
		return s, events, ErrNotDeferred
	}
	events = append(events, Event{Kind: EventDeferralCleared, Date: today, Occurrence: s.DeferredFrom, Slot: s.Slot, From: s.Due, To: s.DeferredFrom})
	s.Due, s.Deferred, s.DeferredFrom = s.DeferredFrom, false, Date{}
	// The restored date may already have passed.
	s, more := t.Catchup(s, today)
	return s, append(events, more...), nil
}

// Pause hides the task and stops recording misses. A non-zero until resumes it
// automatically on that date. Pausing a paused task changes its until date.
func (t Task) Pause(s State, today, until Date) (State, []Event, error) {
	s, events := t.Catchup(s, today)
	if s.Done {
		return s, events, ErrDone
	}
	if !until.IsZero() && !until.After(today) {
		return s, events, ErrPastDate
	}
	s.Paused, s.PauseUntil = true, until
	return s, append(events, Event{Kind: EventPaused, Date: today, Slot: s.Slot, To: until}), nil
}

// Resume un-pauses the task as of today.
func (t Task) Resume(s State, today Date) (State, []Event, error) {
	s, events := t.Catchup(s, today)
	if !s.Paused {
		return s, events, ErrNotPaused
	}
	s, ev := t.resume(s, today)
	return s, append(events, ev), nil
}

func (t Task) resume(s State, d Date) (State, Event) {
	s.Paused, s.PauseUntil = false, Date{}
	switch t.Kind {
	case KindInterval:
		// The time spent paused doesn't count.
		s.Due, s.Deferred, s.DeferredFrom = t.Interval.After(d), false, Date{}
	case KindFixed:
		// Dates inside the pause aren't recorded.
		s = t.newOccurrence(s, t.Recurrence.OnOrAfter(d))
	case KindCycle:
		// Same slot, next rule date.
		s.Due, s.Deferred, s.DeferredFrom = t.Recurrence.OnOrAfter(d), false, Date{}
		s.Done = s.Due.IsZero()
	}
	return s, Event{Kind: EventResumed, Date: d, Slot: s.Slot}
}

// Reactivate brings back a task that was set aside (archived) without
// recording what it missed: a fixed task whose date has passed moves to its
// next rule date on or after today. Other kinds are unchanged; they show as
// overdue instead.
func (t Task) Reactivate(s State, today Date) State {
	s = s.clone()
	if t.Kind == KindFixed && !s.Done && !s.Paused && s.Due.Before(today) {
		s = t.newOccurrence(s, t.Recurrence.OnOrAfter(today))
	}
	return s
}

// SetSlot makes slot the current slot of a cycle without completing anything.
func (t Task) SetSlot(s State, today Date, slot SlotID) (State, []Event, error) {
	s, events, err := t.actionable(s, today)
	if err != nil {
		return s, events, err
	}
	if t.Kind != KindCycle {
		return s, events, ErrNotCycle
	}
	if !slices.Contains(t.Slots, slot) {
		return s, events, ErrUnknownSlot
	}
	s.Slot = slot
	return s, append(events, Event{Kind: EventSlotSet, Date: today, Occurrence: s.Occurrence(), Slot: slot}), nil
}

// Check checks a checklist item on date d (zero means today). Checking the
// last item completes the occurrence, dated by the latest check.
func (t Task) Check(s State, today Date, item ItemID, d Date) (State, []Event, error) {
	s, events, err := t.actionable(s, today)
	if err != nil {
		return s, events, err
	}
	if !slices.Contains(t.Checklist, item) {
		return s, events, ErrUnknownItem
	}
	if d.IsZero() {
		d = today
	}
	if d.After(today) {
		return s, events, ErrFutureDate
	}
	if s.Checks == nil {
		s.Checks = map[ItemID]Date{}
	}
	s.Checks[item] = d
	if _, unchecked := t.splitChecklist(s); len(unchecked) > 0 {
		return s, events, nil
	}
	var last Date
	for _, checked := range s.Checks {
		last = MaxDate(last, checked)
	}
	s, more, err := t.Complete(s, today, CompleteOptions{Date: last})
	return s, append(events, more...), err
}

// Uncheck unchecks a checklist item.
func (t Task) Uncheck(s State, today Date, item ItemID) (State, []Event, error) {
	s, events, err := t.actionable(s, today)
	if err != nil {
		return s, events, err
	}
	if !slices.Contains(t.Checklist, item) {
		return s, events, ErrUnknownItem
	}
	delete(s.Checks, item)
	return s, events, nil
}

// RecomputeFromHistory resets an interval task's due date after its history
// was edited: lastDone is the latest remaining completion date (zero if none).
// It leaves deferred, paused and non-interval tasks alone.
func (t Task) RecomputeFromHistory(s State, lastDone Date) State {
	if t.Kind != KindInterval || s.Deferred || s.Paused || lastDone.IsZero() {
		return s
	}
	s = s.clone()
	s.Due = t.Interval.After(lastDone)
	return s
}

// Projected is a future occurrence.
type Projected struct {
	Due  Date   `json:"due"`
	Slot SlotID `json:"slot,omitempty"`
}

// Project returns up to n upcoming occurrences, starting with the current
// one, assuming each is done on its due date.
func (t Task) Project(s State, n int) []Projected {
	if s.Done || s.Paused || s.Due.IsZero() {
		return nil
	}
	out := []Projected{{s.Due, s.Slot}}
	if t.Kind == KindOnce {
		return out
	}
	due, slot := s.Due, s.Slot
	for len(out) < n {
		if t.Kind == KindInterval {
			due = t.Interval.After(due)
		} else {
			due = t.Recurrence.After(due)
		}
		if due.IsZero() {
			break
		}
		if t.Kind == KindCycle {
			slot = t.Slots[(slices.Index(t.Slots, slot)+1)%len(t.Slots)]
		}
		out = append(out, Projected{due, slot})
	}
	return out
}

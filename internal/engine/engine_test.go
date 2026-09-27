package engine

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func d(s string) Date { return MustParseDate(s) }

// October 2026: Thursday the 1st; Saturdays 3, 10, 17, 24, 31; Tuesdays 6, 13, 20, 27.
var (
	saturdays = MustParseRecurrence("FREQ=WEEKLY;BYDAY=SA", d("2026-01-03"))
	tuesdays  = MustParseRecurrence("FREQ=WEEKLY;BYDAY=TU", d("2026-01-06"))
)

func interval(n int, u Unit) Task {
	return Task{Kind: KindInterval, Interval: Interval{n, u}, Priority: Normal}
}
func fixed(r *Recurrence) Task { return Task{Kind: KindFixed, Recurrence: r, Priority: Normal} }
func cycle(r *Recurrence, slots ...SlotID) Task {
	return Task{Kind: KindCycle, Recurrence: r, Slots: slots, Priority: Normal}
}

// kinds returns the event kinds, dates and occurrences as compact strings.
func summarize(events []Event) []string {
	var out []string
	for _, e := range events {
		s := string(e.Kind) + " " + e.Date.String()
		if e.Occurrence != e.Date && !e.Occurrence.IsZero() {
			s += " (occ " + e.Occurrence.String() + ")"
		}
		if e.Merged {
			s += " merged"
		}
		if e.Slot != "" {
			s += " " + string(e.Slot)
		}
		out = append(out, s)
	}
	return out
}

func TestDateArithmetic(t *testing.T) {
	assert.Equal(t, d("2026-02-28"), d("2026-01-31").AddMonths(1))
	assert.Equal(t, d("2028-02-29"), d("2028-01-31").AddMonths(1))
	assert.Equal(t, d("2026-03-31"), d("2026-01-31").AddMonths(2))
	assert.Equal(t, d("2025-12-31"), d("2026-01-31").AddMonths(-1))
	assert.Equal(t, d("2027-02-28"), d("2028-02-29").AddYears(-1))
	assert.Equal(t, d("2027-01-15"), d("2026-10-15").AddMonths(3))
	assert.Equal(t, 7, d("2026-10-03").DaysUntil(d("2026-10-10")))
	assert.Equal(t, -3, d("2026-10-03").DaysUntil(d("2026-09-30")))
}

func TestRecurrence(t *testing.T) {
	assert.Equal(t, d("2026-10-10"), saturdays.After(d("2026-10-03")))
	assert.Equal(t, d("2026-10-03"), saturdays.OnOrAfter(d("2026-10-03")))
	assert.Equal(t, d("2026-10-03"), saturdays.OnOrAfter(d("2026-09-29")))
	assert.Equal(t, []Date{d("2026-10-10"), d("2026-10-17")}, saturdays.Between(d("2026-10-03"), d("2026-10-17")))
	assert.True(t, saturdays.Contains(d("2026-10-10")))
	assert.False(t, saturdays.Contains(d("2026-10-11")))

	firstSat := MustParseRecurrence("RRULE:FREQ=MONTHLY;BYDAY=1SA", d("2026-01-01"))
	assert.Equal(t, d("2026-11-07"), firstSat.After(d("2026-10-03")))

	limited := MustParseRecurrence("FREQ=DAILY;COUNT=2", d("2026-10-01"))
	assert.Equal(t, d("2026-10-02"), limited.After(d("2026-10-01")))
	assert.True(t, limited.After(d("2026-10-02")).IsZero())

	for _, bad := range []string{"FREQ=HOURLY", "FREQ=DAILY;BYHOUR=3", "nonsense", "DTSTART:20260101\nFREQ=DAILY"} {
		_, err := ParseRecurrence(bad, d("2026-01-01"))
		assert.Error(t, err, bad)
	}
}

func TestValidate(t *testing.T) {
	assert.NoError(t, interval(3, Months).Validate())
	assert.Error(t, interval(0, Months).Validate())
	assert.Error(t, Task{Kind: KindFixed, Priority: Normal}.Validate())
	assert.Error(t, cycle(saturdays).Validate())
	assert.Error(t, cycle(saturdays, "a", "a").Validate())
	c := cycle(saturdays, "a")
	c.Checklist = []ItemID{"x"}
	assert.Error(t, c.Validate())
	i := interval(1, Days)
	i.Priority = 4
	assert.Error(t, i.Validate())
}

func TestInterval(t *testing.T) {
	task := interval(3, Months)
	s := task.Init(d("2026-10-01"), d("2026-11-15"))
	assert.Equal(t, d("2026-11-15"), s.Due)

	// Early: the next due date counts from the completion.
	s2, ev, err := task.Complete(s, d("2026-11-01"), CompleteOptions{})
	require.NoError(t, err)
	assert.Equal(t, d("2027-02-01"), s2.Due)
	assert.Equal(t, []string{"done 2026-11-01 (occ 2026-11-15)"}, summarize(ev))

	// Late: never missed, just overdue; completing pushes everything back.
	s3, ev := task.Catchup(s, d("2027-01-10"))
	assert.Empty(t, ev)
	s3, _, err = task.Complete(s3, d("2027-01-10"), CompleteOptions{})
	require.NoError(t, err)
	assert.Equal(t, d("2027-04-10"), s3.Due)

	// Recording a past completion.
	s4, _, err := task.Complete(s, d("2026-10-20"), CompleteOptions{Date: d("2026-10-05")})
	require.NoError(t, err)
	assert.Equal(t, d("2027-01-05"), s4.Due)

	_, _, err = task.Complete(s, d("2026-10-20"), CompleteOptions{Date: d("2026-10-21")})
	assert.ErrorIs(t, err, ErrFutureDate)
}

func TestFixedEarlyDoesNotShift(t *testing.T) {
	task := fixed(tuesdays)
	s := task.Init(d("2026-10-01"), Date{})
	assert.Equal(t, d("2026-10-06"), s.Due)

	s, ev, err := task.Complete(s, d("2026-10-05"), CompleteOptions{})
	require.NoError(t, err)
	assert.Equal(t, d("2026-10-13"), s.Due)
	assert.Equal(t, []string{"done 2026-10-05 (occ 2026-10-06)"}, summarize(ev))
}

func TestFixedMissed(t *testing.T) {
	task := fixed(tuesdays)
	s := task.Init(d("2026-10-01"), Date{})

	// Due today is not missed yet.
	s1, ev := task.Catchup(s, d("2026-10-06"))
	assert.Empty(t, ev)
	assert.Equal(t, d("2026-10-06"), s1.Due)

	// Three weeks away: each passed Tuesday is its own miss.
	s2, ev := task.Catchup(s, d("2026-10-22"))
	assert.Equal(t, []string{"missed 2026-10-06", "missed 2026-10-13", "missed 2026-10-20"}, summarize(ev))
	assert.Equal(t, d("2026-10-27"), s2.Due)

	// Idempotent.
	s3, ev := task.Catchup(s2, d("2026-10-22"))
	assert.Empty(t, ev)
	assert.Equal(t, s2, s3)

	// Landing exactly on a rule date makes it the current occurrence.
	s4, ev := task.Catchup(s, d("2026-10-13"))
	assert.Equal(t, []string{"missed 2026-10-06"}, summarize(ev))
	assert.Equal(t, d("2026-10-13"), s4.Due)

	// Actions catch up first.
	s5, ev, err := task.Complete(s, d("2026-10-13"), CompleteOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"missed 2026-10-06", "done 2026-10-13"}, summarize(ev))
	assert.Equal(t, d("2026-10-20"), s5.Due)
}

func TestFixedSkip(t *testing.T) {
	task := fixed(tuesdays)
	s := task.Init(d("2026-10-01"), Date{})
	s, ev, err := task.Skip(s, d("2026-10-02"))
	require.NoError(t, err)
	assert.Equal(t, []string{"skipped 2026-10-02 (occ 2026-10-06)"}, summarize(ev))
	assert.Equal(t, d("2026-10-13"), s.Due)
}

func TestFixedDeferMerges(t *testing.T) {
	task := fixed(tuesdays)
	s := task.Init(d("2026-10-01"), Date{})

	// A short deferral that doesn't reach the next Tuesday.
	short, _, err := task.Defer(s, d("2026-10-05"), d("2026-10-08"))
	require.NoError(t, err)
	_, ev := task.Catchup(short, d("2026-10-08"))
	assert.Empty(t, ev, "not missed until the deferred date passes")
	done, ev, err := task.Complete(short, d("2026-10-08"), CompleteOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"done 2026-10-08 (occ 2026-10-06)"}, summarize(ev))
	assert.Equal(t, d("2026-10-13"), done.Due)

	// Deferring past later Tuesdays merges them.
	long, ev, err := task.Defer(s, d("2026-10-05"), d("2026-10-15"))
	require.NoError(t, err)
	assert.Equal(t, []string{"deferred 2026-10-05 (occ 2026-10-06)"}, summarize(ev))
	assert.Equal(t, d("2026-10-06"), long.DeferredFrom)

	done, ev, err = task.Complete(long, d("2026-10-14"), CompleteOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"done 2026-10-14 (occ 2026-10-06)", "skipped 2026-10-13 merged"}, summarize(ev))
	assert.Equal(t, d("2026-10-20"), done.Due)

	// Deferred onto a rule date: that date merges too.
	onRule, _, _ := task.Defer(s, d("2026-10-05"), d("2026-10-13"))
	done, ev, _ = task.Complete(onRule, d("2026-10-13"), CompleteOptions{})
	assert.Equal(t, []string{"done 2026-10-13 (occ 2026-10-06)", "skipped 2026-10-13 merged"}, summarize(ev))
	assert.Equal(t, d("2026-10-20"), done.Due)

	// Missing a deferred occurrence.
	missed, ev := task.Catchup(long, d("2026-10-23"))
	assert.Equal(t, []string{"missed 2026-10-15 (occ 2026-10-06)", "skipped 2026-10-13 merged", "missed 2026-10-20"}, summarize(ev))
	assert.Equal(t, d("2026-10-27"), missed.Due)
	assert.False(t, missed.Deferred)
}

func TestClearDeferral(t *testing.T) {
	task := fixed(tuesdays)
	s := task.Init(d("2026-10-01"), Date{})
	s, _, _ = task.Defer(s, d("2026-10-05"), d("2026-10-15"))

	back, ev, err := task.ClearDeferral(s, d("2026-10-05"))
	require.NoError(t, err)
	assert.Equal(t, d("2026-10-06"), back.Due)
	assert.False(t, back.Deferred)
	assert.Equal(t, []string{"deferral_cleared 2026-10-05 (occ 2026-10-06)"}, summarize(ev))

	// Clearing after the original date passed records it as missed.
	late, ev, err := task.ClearDeferral(s, d("2026-10-08"))
	require.NoError(t, err)
	assert.Equal(t, []string{"deferral_cleared 2026-10-08 (occ 2026-10-06)", "missed 2026-10-06"}, summarize(ev))
	assert.Equal(t, d("2026-10-13"), late.Due)

	_, _, err = task.ClearDeferral(back, d("2026-10-05"))
	assert.ErrorIs(t, err, ErrNotDeferred)
	_, _, err = task.Defer(back, d("2026-10-05"), d("2026-10-04"))
	assert.ErrorIs(t, err, ErrPastDate)
}

func TestIntervalDefer(t *testing.T) {
	// "Push off dryer vent cleaning for a month."
	task := interval(6, Months)
	s := task.Init(d("2026-10-01"), d("2026-10-01"))
	s, _, err := task.Defer(s, d("2026-10-01"), d("2026-11-01"))
	require.NoError(t, err)
	assert.Equal(t, d("2026-11-01"), s.Due)

	// Re-deferring keeps the original schedule date.
	s, _, _ = task.Defer(s, d("2026-10-20"), d("2026-11-15"))
	assert.Equal(t, d("2026-10-01"), s.DeferredFrom)

	// Completion recalculates from when it was done.
	s, ev, err := task.Complete(s, d("2026-11-10"), CompleteOptions{})
	require.NoError(t, err)
	assert.Equal(t, d("2027-05-10"), s.Due)
	assert.False(t, s.Deferred)
	assert.Equal(t, []string{"done 2026-11-10 (occ 2026-10-01)"}, summarize(ev))
}

func TestCycle(t *testing.T) {
	task := cycle(saturdays, "bedrooms", "bathrooms")
	start := task.Init(d("2026-10-04"), Date{})
	assert.Equal(t, State{Due: d("2026-10-10"), Slot: "bedrooms"}, start)

	cases := []struct {
		name    string
		doneOn  Date
		wantDue Date
	}{
		{"on the day", d("2026-10-10"), d("2026-10-17")},
		{"early", d("2026-10-09"), d("2026-10-17")},
		{"missed weekend, done Wednesday: recovers", d("2026-10-14"), d("2026-10-17")},
		{"done late on the following Saturday", d("2026-10-17"), d("2026-10-24")},
		{"three weeks late", d("2026-11-04"), d("2026-11-07")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Misses carry over silently.
			caught, ev := task.Catchup(start, tc.doneOn)
			assert.Empty(t, ev)
			assert.Equal(t, start, caught)

			s, ev, err := task.Complete(start, tc.doneOn, CompleteOptions{})
			require.NoError(t, err)
			assert.Equal(t, tc.wantDue, s.Due)
			assert.Equal(t, SlotID("bathrooms"), s.Slot)
			assert.Equal(t, SlotID("bedrooms"), ev[0].Slot)
		})
	}

	// Wraps around.
	s, _, _ := task.Complete(start, d("2026-10-10"), CompleteOptions{})
	s, _, _ = task.Complete(s, d("2026-10-17"), CompleteOptions{})
	assert.Equal(t, State{Due: d("2026-10-24"), Slot: "bedrooms"}, s)
}

func TestCycleOverride(t *testing.T) {
	task := cycle(saturdays, "A", "B", "C")
	s := task.Init(d("2026-10-04"), Date{})
	require.Equal(t, SlotID("A"), s.Slot)

	// A is due but we do C: the rotation continues after C.
	s2, ev, err := task.Complete(s, d("2026-10-10"), CompleteOptions{AsSlot: "C"})
	require.NoError(t, err)
	assert.Equal(t, SlotID("C"), ev[0].Slot)
	assert.Equal(t, State{Due: d("2026-10-17"), Slot: "A"}, s2)

	s3, ev, err := task.SetSlot(s, d("2026-10-05"), "B")
	require.NoError(t, err)
	assert.Equal(t, State{Due: d("2026-10-10"), Slot: "B"}, s3)
	assert.Equal(t, []string{"slot_set 2026-10-05 (occ 2026-10-10) B"}, summarize(ev))

	_, _, err = task.SetSlot(s, d("2026-10-05"), "Z")
	assert.ErrorIs(t, err, ErrUnknownSlot)
	_, _, err = fixed(tuesdays).SetSlot(State{Due: d("2026-10-06")}, d("2026-10-05"), "A")
	assert.ErrorIs(t, err, ErrNotCycle)

	// Skipping a slot moves on to the next one.
	s4, _, err := task.Skip(s, d("2026-10-10"))
	require.NoError(t, err)
	assert.Equal(t, State{Due: d("2026-10-17"), Slot: "B"}, s4)
}

func TestCycleDefer(t *testing.T) {
	task := cycle(saturdays, "A", "B")
	s := task.Init(d("2026-10-04"), Date{})
	s, _, err := task.Defer(s, d("2026-10-05"), d("2026-10-20"))
	require.NoError(t, err)
	s, _, err = task.Complete(s, d("2026-10-12"), CompleteOptions{})
	require.NoError(t, err)
	assert.Equal(t, State{Due: d("2026-10-24"), Slot: "B"}, s, "next rule date after max(done, deferred date)")
}

func TestOnce(t *testing.T) {
	task := Task{Kind: KindOnce, Priority: Normal}
	s := task.Init(d("2026-10-01"), Date{})
	assert.True(t, s.Due.IsZero())
	_, ok := task.Rank(s, d("2026-10-01"))
	assert.False(t, ok, "no due date: not in Upcoming")

	s, ev, err := task.Complete(s, d("2026-10-02"), CompleteOptions{})
	require.NoError(t, err)
	assert.True(t, s.Done)
	assert.Equal(t, []string{"done 2026-10-02"}, summarize(ev))

	_, _, err = task.Complete(s, d("2026-10-03"), CompleteOptions{})
	assert.ErrorIs(t, err, ErrDone)

	// Deferring a once task without a due date gives it one.
	s = task.Init(d("2026-10-01"), Date{})
	s, _, err = task.Defer(s, d("2026-10-01"), d("2026-10-09"))
	require.NoError(t, err)
	assert.Equal(t, d("2026-10-09"), s.Due)
	s, _, _ = task.ClearDeferral(s, d("2026-10-02"))
	assert.True(t, s.Due.IsZero())
}

func TestRecurrenceEnds(t *testing.T) {
	task := fixed(MustParseRecurrence("FREQ=WEEKLY;BYDAY=TU;COUNT=2", d("2026-10-06")))
	s := task.Init(d("2026-10-01"), Date{})
	s, _, _ = task.Complete(s, d("2026-10-06"), CompleteOptions{})
	assert.Equal(t, d("2026-10-13"), s.Due)
	s, _, _ = task.Complete(s, d("2026-10-13"), CompleteOptions{})
	assert.True(t, s.Done)
	s, ev := task.Catchup(s, d("2026-12-01"))
	assert.Empty(t, ev)
	assert.True(t, s.Done)
}

func TestPauseResume(t *testing.T) {
	t.Run("interval: resume date + N", func(t *testing.T) {
		task := interval(1, Months)
		s := task.Init(d("2026-10-01"), d("2026-10-05"))
		s, _, err := task.Pause(s, d("2026-10-03"), Date{})
		require.NoError(t, err)
		_, _, err = task.Complete(s, d("2026-10-04"), CompleteOptions{})
		assert.ErrorIs(t, err, ErrPaused)
		_, ok := task.Rank(s, d("2026-10-05"))
		assert.False(t, ok)

		s, ev, err := task.Resume(s, d("2027-03-15"))
		require.NoError(t, err)
		assert.Equal(t, d("2027-04-15"), s.Due)
		assert.Equal(t, []string{"resumed 2027-03-15"}, summarize(ev))
	})

	t.Run("fixed: no misses while paused", func(t *testing.T) {
		task := fixed(tuesdays)
		s := task.Init(d("2026-10-01"), Date{})
		s, _, _ = task.Pause(s, d("2026-10-02"), Date{})
		s, ev := task.Catchup(s, d("2026-10-29"))
		assert.Empty(t, ev)
		s, ev, err := task.Resume(s, d("2026-10-29"))
		require.NoError(t, err)
		assert.Equal(t, []string{"resumed 2026-10-29"}, summarize(ev))
		assert.Equal(t, d("2026-11-03"), s.Due)
	})

	t.Run("cycle: same slot", func(t *testing.T) {
		task := cycle(saturdays, "A", "B")
		s := task.Init(d("2026-10-04"), Date{})
		s, _, _ = task.Complete(s, d("2026-10-10"), CompleteOptions{})
		s, _, _ = task.Pause(s, d("2026-10-11"), Date{})
		s, _, _ = task.Resume(s, d("2026-11-02"))
		assert.Equal(t, State{Due: d("2026-11-07"), Slot: "B"}, s)
	})

	t.Run("auto-resume, then misses after it", func(t *testing.T) {
		task := fixed(tuesdays)
		s := task.Init(d("2026-10-01"), Date{})
		s, _, err := task.Pause(s, d("2026-10-02"), d("2026-10-12"))
		require.NoError(t, err)
		s, ev := task.Catchup(s, d("2026-10-11"))
		assert.Empty(t, ev)
		assert.True(t, s.Paused)
		s, ev = task.Catchup(s, d("2026-10-15"))
		assert.Equal(t, []string{"resumed 2026-10-12", "missed 2026-10-13"}, summarize(ev))
		assert.Equal(t, d("2026-10-20"), s.Due)
		assert.False(t, s.Paused)
	})

	t.Run("errors", func(t *testing.T) {
		task := interval(1, Weeks)
		s := task.Init(d("2026-10-01"), Date{})
		_, _, err := task.Resume(s, d("2026-10-01"))
		assert.ErrorIs(t, err, ErrNotPaused)
		_, _, err = task.Pause(s, d("2026-10-02"), d("2026-10-02"))
		assert.ErrorIs(t, err, ErrPastDate)
	})
}

func TestChecklist(t *testing.T) {
	task := interval(1, Years)
	task.Checklist = []ItemID{"hall", "bedroom", "garage"}
	s := task.Init(d("2026-10-01"), d("2026-10-01"))

	s, ev, err := task.Check(s, d("2026-10-01"), "hall", Date{})
	require.NoError(t, err)
	assert.Empty(t, ev)

	_, _, err = task.Complete(s, d("2026-10-02"), CompleteOptions{})
	assert.ErrorIs(t, err, ErrChecklistIncomplete)
	_, _, err = task.Check(s, d("2026-10-02"), "attic", Date{})
	assert.ErrorIs(t, err, ErrUnknownItem)

	// Uncheck and recheck, then finish: completion is dated by the latest
	// check, so the group resets together.
	s, _, _ = task.Uncheck(s, d("2026-10-02"), "hall")
	assert.Empty(t, s.Checks)
	s, _, _ = task.Check(s, d("2026-10-02"), "hall", d("2026-10-01"))
	s, _, _ = task.Check(s, d("2026-12-20"), "garage", d("2026-12-15"))
	s, ev, err = task.Check(s, d("2026-12-20"), "bedroom", d("2026-12-10"))
	require.NoError(t, err)
	require.Len(t, ev, 1)
	assert.Equal(t, EventDone, ev[0].Kind)
	assert.Equal(t, d("2026-12-15"), ev[0].Date)
	assert.Equal(t, []ItemID{"hall", "bedroom", "garage"}, ev[0].Checked)
	assert.Equal(t, d("2027-12-15"), s.Due)
	assert.Empty(t, s.Checks, "new occurrence starts unchecked")
}

func TestChecklistForceAndMissed(t *testing.T) {
	task := interval(1, Years)
	task.Checklist = []ItemID{"a", "b"}
	s := task.Init(d("2026-10-01"), d("2026-10-01"))
	s, _, _ = task.Check(s, d("2026-10-01"), "a", Date{})

	forced, ev, err := task.Complete(s, d("2026-10-02"), CompleteOptions{Force: true})
	require.NoError(t, err)
	assert.Equal(t, []ItemID{"a"}, ev[0].Checked)
	assert.Equal(t, []ItemID{"b"}, ev[0].Unchecked)
	assert.Equal(t, d("2027-10-02"), forced.Due)

	ft := fixed(tuesdays)
	ft.Checklist = []ItemID{"a", "b"}
	fs := ft.Init(d("2026-10-01"), Date{})
	fs, _, _ = ft.Check(fs, d("2026-10-01"), "b", Date{})
	fs, ev = ft.Catchup(fs, d("2026-10-07"))
	require.Len(t, ev, 1)
	assert.Equal(t, []ItemID{"b"}, ev[0].Checked)
	assert.Equal(t, []ItemID{"a"}, ev[0].Unchecked)
	assert.Empty(t, fs.Checks)
}

func TestPurity(t *testing.T) {
	task := interval(1, Years)
	task.Checklist = []ItemID{"a", "b"}
	s := task.Init(d("2026-10-01"), d("2026-10-01"))
	s, _, _ = task.Check(s, d("2026-10-01"), "a", Date{})
	before := s.clone()
	_, _, _ = task.Check(s, d("2026-10-01"), "b", Date{})
	_, _, _ = task.Uncheck(s, d("2026-10-01"), "a")
	assert.Equal(t, before, s, "actions must not modify their input")
}

func TestRecomputeFromHistory(t *testing.T) {
	task := interval(2, Weeks)
	s := State{Due: d("2026-10-20")}
	assert.Equal(t, d("2026-10-15"), task.RecomputeFromHistory(s, d("2026-10-01")).Due)
	s.Deferred, s.DeferredFrom = true, d("2026-10-14")
	assert.Equal(t, d("2026-10-20"), task.RecomputeFromHistory(s, d("2026-10-01")).Due)
}

func TestProject(t *testing.T) {
	task := cycle(saturdays, "A", "B")
	s := task.Init(d("2026-10-04"), Date{})
	assert.Equal(t, []Projected{
		{d("2026-10-10"), "A"}, {d("2026-10-17"), "B"}, {d("2026-10-24"), "A"},
	}, task.Project(s, 3))

	it := interval(1, Months)
	assert.Equal(t, []Projected{{d("2026-01-31"), ""}, {d("2026-02-28"), ""}},
		it.Project(State{Due: d("2026-01-31")}, 2))
}

func TestFixedCarryOver(t *testing.T) {
	// Heartworm meds on the 1st: stays overdue until done, doesn't drift.
	task := fixed(MustParseRecurrence("FREQ=MONTHLY;BYMONTHDAY=1", d("2026-01-01")))
	task.CarryOver = true
	require.NoError(t, task.Validate())
	start := task.Init(d("2026-09-20"), Date{})
	assert.Equal(t, d("2026-10-01"), start.Due)

	// Missed dates aren't recorded; it stays due Oct 1 (overdue).
	s, ev := task.Catchup(start, d("2026-11-15"))
	assert.Empty(t, ev)
	assert.Equal(t, d("2026-10-01"), s.Due)
	u, ok := task.Rank(s, d("2026-10-04"))
	assert.True(t, ok)
	assert.Equal(t, GroupOverdue, u.Group)

	cases := []struct {
		name    string
		doneOn  Date
		wantDue Date
	}{
		{"on time", d("2026-10-01"), d("2026-11-01")},
		{"early", d("2026-09-29"), d("2026-11-01")},
		{"three days late: next is still the 1st", d("2026-10-04"), d("2026-11-01")},
		{"six weeks late", d("2026-11-15"), d("2026-12-01")},
	}
	for _, tc := range cases {
		s, ev, err := task.Complete(start, tc.doneOn, CompleteOptions{})
		require.NoError(t, err, tc.name)
		assert.Equal(t, tc.wantDue, s.Due, tc.name)
		assert.Equal(t, EventDone, ev[0].Kind, tc.name)
		assert.Equal(t, d("2026-10-01"), ev[0].Occurrence, tc.name)
	}

	// Deferring past later dates doesn't record merged skips.
	deferred, _, err := task.Defer(start, d("2026-09-30"), d("2026-11-10"))
	require.NoError(t, err)
	s, ev, err = task.Complete(deferred, d("2026-11-09"), CompleteOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"done 2026-11-09 (occ 2026-10-01)"}, summarize(ev))
	assert.Equal(t, d("2026-12-01"), s.Due)

	// Only fixed tasks can carry over.
	bad := interval(1, Months)
	bad.CarryOver = true
	assert.Error(t, bad.Validate())
}

package engine

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLeadDays(t *testing.T) {
	today := d("2026-10-01")
	cases := []struct {
		task Task
		want int
	}{
		{interval(1, Days), 1},
		{interval(1, Weeks), 2},
		{interval(1, Months), 8},
		{interval(1, Years), 14},
		{fixed(tuesdays), 2},
		{fixed(MustParseRecurrence("FREQ=MONTHLY;BYDAY=1SA", d("2026-01-01"))), 7}, // gaps of 28 or 35 days
		{Task{Kind: KindOnce}, 2},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, tc.task.LeadDays(today), "%+v", tc.task)
	}
	custom := interval(1, Years)
	custom.Lead = 30
	assert.Equal(t, 30, custom.LeadDays(today))
}

func TestRank(t *testing.T) {
	today := d("2026-10-01")
	yearly, weekly := interval(1, Years), interval(1, Weeks)

	// Not yet in the window.
	_, ok := yearly.Rank(State{Due: today.AddDays(15)}, today)
	assert.False(t, ok)

	// The design's headline case: a yearly task a week out ranks with a weekly
	// task due tomorrow.
	y, ok := yearly.Rank(State{Due: today.AddDays(7)}, today)
	assert.True(t, ok)
	w, _ := weekly.Rank(State{Due: today.AddDays(1)}, today)
	assert.Equal(t, GroupSoon, y.Group)
	assert.InDelta(t, 0.5, y.Score, 1e-9)
	assert.InDelta(t, 0.5, w.Score, 1e-9)

	due, _ := weekly.Rank(State{Due: today}, today)
	assert.Equal(t, Urgency{Group: GroupToday, DaysUntil: 0, LeadDays: 2, U: 1, Score: 1}, due)

	// Lateness is measured against each task's lead time.
	wLate, _ := weekly.Rank(State{Due: today.AddDays(-3)}, today)
	yLate, _ := yearly.Rank(State{Due: today.AddDays(-3)}, today)
	assert.Equal(t, GroupOverdue, wLate.Group)
	assert.InDelta(t, 2.5, wLate.Score, 1e-9)
	assert.Greater(t, wLate.Score, yLate.Score)

	// Priority weights.
	high := weekly
	high.Priority = High
	h, _ := high.Rank(State{Due: today}, today)
	assert.InDelta(t, 1.5, h.Score, 1e-9)

	_, ok = weekly.Rank(State{Due: today, Paused: true}, today)
	assert.False(t, ok)
}

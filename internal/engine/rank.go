package engine

import (
	"math"
	"slices"
)

// Ranking constants. See docs/design.md §5.
const (
	minDefaultLead = 1
	maxDefaultLead = 14
	// periodSamples is how many upcoming rule dates are used to estimate the
	// period of fixed and cycle tasks.
	periodSamples = 6
	// fallbackPeriod is used for once tasks and rules with too few dates.
	oncePeriod     = 7
	fallbackPeriod = 365
)

var priorityWeight = map[Priority]float64{High: 1.5, Normal: 1.0, Low: 0.6}

// PeriodDays estimates how often the task recurs, in days.
func (t Task) PeriodDays(today Date) float64 {
	switch t.Kind {
	case KindInterval:
		return t.Interval.ApproxDays()
	case KindOnce:
		return oncePeriod
	}
	dates := []Date{t.Recurrence.OnOrAfter(today)}
	for len(dates) < periodSamples && !dates[len(dates)-1].IsZero() {
		dates = append(dates, t.Recurrence.After(dates[len(dates)-1]))
	}
	var gaps []float64
	for i := 1; i < len(dates) && !dates[i].IsZero(); i++ {
		gaps = append(gaps, float64(dates[i-1].DaysUntil(dates[i])))
	}
	if len(gaps) == 0 {
		return fallbackPeriod
	}
	slices.Sort(gaps)
	if n := len(gaps); n%2 == 0 {
		return (gaps[n/2-1] + gaps[n/2]) / 2
	}
	return gaps[len(gaps)/2]
}

// LeadDays is how many days before its due date the task shows up in
// Upcoming: the task's own lead time, or round(period/4) clamped to 1–14.
func (t Task) LeadDays(today Date) int {
	if t.Lead > 0 {
		return t.Lead
	}
	lead := int(math.Round(t.PeriodDays(today) / 4))
	return min(max(lead, minDefaultLead), maxDefaultLead)
}

type Group string

const (
	GroupOverdue Group = "overdue"
	GroupToday   Group = "today"
	GroupSoon    Group = "soon"
)

// Urgency is where a task sits in the Upcoming view.
type Urgency struct {
	Group     Group
	DaysUntil int // negative when overdue
	LeadDays  int
	// U runs from 0 (the lead window opens) to 1 (due today) and keeps
	// growing, by 1 per lead time, once overdue.
	U float64
	// Score is U weighted by priority. Higher is more urgent.
	Score float64
}

// Rank returns the task's urgency, and false if it doesn't belong in Upcoming
// (paused, done, no due date, or not yet within its lead time).
func (t Task) Rank(s State, today Date) (Urgency, bool) {
	if s.Done || s.Paused || s.Due.IsZero() {
		return Urgency{}, false
	}
	lead := t.LeadDays(today)
	d := today.DaysUntil(s.Due)
	if d > lead {
		return Urgency{}, false
	}
	u := Urgency{DaysUntil: d, LeadDays: lead}
	switch {
	case d < 0:
		u.Group, u.U = GroupOverdue, 1+float64(-d)/float64(lead)
	case d == 0:
		u.Group, u.U = GroupToday, 1
	default:
		u.Group, u.U = GroupSoon, 1-float64(d)/float64(lead)
	}
	u.Score = u.U * priorityWeight[t.Priority]
	return u, true
}

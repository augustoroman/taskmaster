package engine

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/teambition/rrule-go"
)

// Kind is the schedule kind of a task.
type Kind string

const (
	// KindInterval recurs N units after each completion.
	KindInterval Kind = "interval"
	// KindFixed recurs on calendar dates; missed occurrences are recorded and skipped.
	KindFixed Kind = "fixed"
	// KindCycle rotates through slots on calendar dates; misses carry over.
	KindCycle Kind = "cycle"
	// KindOnce happens once, with an optional due date.
	KindOnce Kind = "once"
)

type Unit string

const (
	Days   Unit = "days"
	Weeks  Unit = "weeks"
	Months Unit = "months"
	Years  Unit = "years"
)

// Interval is "every N units".
type Interval struct {
	N    int  `json:"n"`
	Unit Unit `json:"unit"`
}

func (i Interval) Validate() error {
	if i.N < 1 {
		return fmt.Errorf("interval must be at least 1, got %d", i.N)
	}
	switch i.Unit {
	case Days, Weeks, Months, Years:
		return nil
	}
	return fmt.Errorf("unknown interval unit %q", i.Unit)
}

// After returns d plus the interval.
func (i Interval) After(d Date) Date {
	switch i.Unit {
	case Days:
		return d.AddDays(i.N)
	case Weeks:
		return d.AddDays(7 * i.N)
	case Months:
		return d.AddMonths(i.N)
	case Years:
		return d.AddYears(i.N)
	}
	panic(fmt.Sprintf("unknown interval unit %q", i.Unit))
}

// ApproxDays is the interval's length for ranking (1 month = 30 days).
func (i Interval) ApproxDays() float64 {
	perUnit := map[Unit]float64{Days: 1, Weeks: 7, Months: 30, Years: 365}[i.Unit]
	return float64(i.N) * perUnit
}

// Recurrence is an iCalendar RRULE anchored at a start date. Only date-level
// rules are allowed (no HOURLY/MINUTELY/SECONDLY, no BYHOUR etc).
type Recurrence struct {
	rule  string
	start Date
	r     *rrule.RRule
}

func ParseRecurrence(rule string, start Date) (*Recurrence, error) {
	if start.IsZero() {
		return nil, errors.New("recurrence needs a start date")
	}
	rule = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rule), "RRULE:"))
	if strings.ContainsAny(rule, "\n\r") || strings.Contains(strings.ToUpper(rule), "DTSTART") {
		return nil, errors.New("recurrence rule must be a single RRULE line without DTSTART")
	}
	opt, err := rrule.StrToROption(rule)
	if err != nil {
		return nil, fmt.Errorf("invalid recurrence rule: %w", err)
	}
	if opt.Freq > rrule.DAILY {
		return nil, errors.New("recurrence rule must be DAILY or less frequent")
	}
	if len(opt.Byhour) > 0 || len(opt.Byminute) > 0 || len(opt.Bysecond) > 0 {
		return nil, errors.New("recurrence rule must not set a time of day")
	}
	if !opt.Until.IsZero() {
		opt.Until = DateOf(opt.Until).Time()
	}
	opt.Dtstart = start.Time()
	r, err := rrule.NewRRule(*opt)
	if err != nil {
		return nil, fmt.Errorf("invalid recurrence rule: %w", err)
	}
	return &Recurrence{rule: rule, start: start, r: r}, nil
}

func MustParseRecurrence(rule string, start Date) *Recurrence {
	r, err := ParseRecurrence(rule, start)
	if err != nil {
		panic(err)
	}
	return r
}

func (r *Recurrence) Rule() string { return r.rule }
func (r *Recurrence) Start() Date  { return r.start }

// After returns the first rule date strictly after d, or the zero Date if the
// rule has ended.
func (r *Recurrence) After(d Date) Date { return r.at(r.r.After(d.Time(), false)) }

// OnOrAfter returns the first rule date on or after d, or the zero Date.
func (r *Recurrence) OnOrAfter(d Date) Date { return r.at(r.r.After(d.Time(), true)) }

// Contains reports whether d is a rule date.
func (r *Recurrence) Contains(d Date) bool { return r.OnOrAfter(d) == d }

// Between returns the rule dates in the half-open range (after, through].
func (r *Recurrence) Between(after, through Date) []Date {
	if !through.After(after) {
		return nil
	}
	var out []Date
	for _, t := range r.r.Between(after.Time(), through.Time(), true) {
		if d := DateOf(t); d.After(after) {
			out = append(out, d)
		}
	}
	return out
}

func (r *Recurrence) at(t time.Time) Date {
	if t.IsZero() {
		return Date{}
	}
	return DateOf(t)
}

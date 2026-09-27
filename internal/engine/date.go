// Package engine implements task scheduling and ranking as pure functions:
// no I/O, no clock. Callers pass in "today" (in the task's time zone) and
// persist the returned state and events. See docs/design.md §4–5.
package engine

import (
	"fmt"
	"time"
)

// Date is a civil date with no time of day or time zone. The zero value means
// "no date".
type Date struct{ t time.Time } // always midnight UTC

const dateLayout = "2006-01-02"

func NewDate(year int, month time.Month, day int) Date {
	return Date{time.Date(year, month, day, 0, 0, 0, 0, time.UTC)}
}

// DateOf returns the calendar date of t in t's location.
func DateOf(t time.Time) Date {
	y, m, d := t.Date()
	return NewDate(y, m, d)
}

// Today returns the current date in loc.
func Today(now time.Time, loc *time.Location) Date { return DateOf(now.In(loc)) }

func ParseDate(s string) (Date, error) {
	if s == "" {
		return Date{}, nil
	}
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		return Date{}, fmt.Errorf("invalid date %q: want YYYY-MM-DD", s)
	}
	return Date{t}, nil
}

func MustParseDate(s string) Date {
	d, err := ParseDate(s)
	if err != nil {
		panic(err)
	}
	return d
}

func (d Date) IsZero() bool { return d.t.IsZero() }

func (d Date) String() string {
	if d.IsZero() {
		return ""
	}
	return d.t.Format(dateLayout)
}

func (d Date) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

func (d *Date) UnmarshalText(b []byte) (err error) {
	*d, err = ParseDate(string(b))
	return err
}

// Time returns midnight UTC of d.
func (d Date) Time() time.Time { return d.t }

func (d Date) Weekday() time.Weekday { return d.t.Weekday() }

func (d Date) AddDays(n int) Date { return Date{d.t.AddDate(0, 0, n)} }

// AddMonths adds n months, clamping to the end of the month:
// Jan 31 + 1 month = Feb 28 (or 29).
func (d Date) AddMonths(n int) Date {
	y, m, day := d.t.Date()
	total := int(m) - 1 + n
	y += floorDiv(total, 12)
	month := time.Month(total - 12*floorDiv(total, 12) + 1)
	if last := daysIn(y, month); day > last {
		day = last
	}
	return NewDate(y, month, day)
}

func (d Date) AddYears(n int) Date { return d.AddMonths(12 * n) }

func (d Date) Compare(o Date) int { return d.t.Compare(o.t) }
func (d Date) Before(o Date) bool { return d.t.Before(o.t) }
func (d Date) After(o Date) bool  { return d.t.After(o.t) }

// DaysUntil returns the number of days from d to o (negative if o is earlier).
func (d Date) DaysUntil(o Date) int { return int(o.t.Sub(d.t).Hours() / 24) }

func MaxDate(a, b Date) Date {
	if a.After(b) {
		return a
	}
	return b
}

func daysIn(y int, m time.Month) int { return time.Date(y, m+1, 0, 0, 0, 0, 0, time.UTC).Day() }

func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

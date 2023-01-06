package schedule

import (
	"fmt"
	"log"
	"time"
)

type Schedule struct {
	Every struct {
		Days   int `json:"days,omitempty"`
		Weeks  int `json:"weeks,omitempty"`
		Months int `json:"months,omitempty"`
		Years  int `json:"years,omitempty"`
	} `json:"every"`
	OnWeekdays OnWeekday `json:"on_weekday,omitempty"`
	Mode       Mode      `json:"mode"`
}

func (s Schedule) Interval() Interval {
	var i Intervals
	if s.Every.Days != 0 {
		i = append(i, Daily(s.Every.Days))
	}
	if s.Every.Weeks != 0 {
		i = append(i, Weekly(s.Every.Weeks))
	}
	if s.Every.Months != 0 {
		i = append(i, Monthly(s.Every.Months))
	}
	if s.Every.Years != 0 {
		i = append(i, Yearly(s.Every.Years))
	}
	if len(i) == 0 {
		log.Println("Error: empty schedule interval")
		return nil
	}
	if len(s.OnWeekdays) != 0 {
		i = append(i, s.OnWeekdays)
	}
	return i
}

func (s Schedule) Next(now, start, last time.Time) time.Time {
	interval := s.Interval()
	if interval == nil {
		return start
	}

	switch s.Mode {
	case FromLast:
		return s.Interval().Next(last)
	case SkipMissed:
		t := start
		for t.Before(now) {
			t = s.Interval().Next(t)
		}
		return t
	}
	panic(fmt.Errorf("unknown mode: %d", s.Mode))
}

type Mode int

const (
	FromLast = Mode(iota)
	SkipMissed
)

func (m Mode) String() string {
	switch m {
	case FromLast:
		return "from-last"
	case SkipMissed:
		return "skip-missed"
	default:
		return "unknown"
	}
}

func (m Mode) MarshalText() (text []byte, err error) {
	return []byte(m.String()), nil
}
func (m *Mode) UnmarshalText(text []byte) error {
	switch string(text) {
	case "from-last":
		*m = FromLast
		return nil
	case "skip-missed":
		*m = SkipMissed
		return nil
	}
	return fmt.Errorf("invalid mode: %#q", text)
}

type Interval interface {
	Next(last time.Time) time.Time
}

type Intervals []Interval

func (list Intervals) Next(last time.Time) time.Time {
	next := last
	for _, i := range list {
		next = i.Next(next)
	}
	return next
}

type OnWeekday map[time.Weekday]bool

func (w OnWeekday) Next(last time.Time) time.Time {
	next := last
	for len(w) > 0 && !w[next.Weekday()] {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

type Daily int

func (d Daily) Next(last time.Time) time.Time { return last.AddDate(0, 0, int(d)) }

type Weekly int

func (w Weekly) Next(last time.Time) time.Time { return last.AddDate(0, 0, 7*int(w)) }

type Monthly int

func (m Monthly) Next(last time.Time) time.Time { return last.AddDate(0, int(m), 0) }

type Yearly int

func (y Yearly) Next(last time.Time) time.Time { return last.AddDate(int(y), 0, 0) }

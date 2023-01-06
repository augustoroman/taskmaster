package schedule

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

var Sunday_July_4_2021 = time.Date(2021, 7, 4, 12, 18, 23, 0, time.Local)

func TestWeekly(t *testing.T) {
	t0 := Sunday_July_4_2021
	assert.Equal(t, t0.AddDate(0, 0, 7), Weekly(1).Next(t0))
	assert.Equal(t, t0.AddDate(0, 0, 21), Weekly(3).Next(t0))
}

func TestOnDayOfWeek(t *testing.T) {
	i := OnWeekday{
		time.Tuesday:  true,
		time.Thursday: true,
	}

	t0 := Sunday_July_4_2021

	// 2 days (Sun --> Tues)
	assert.Equal(t, t0.AddDate(0, 0, 2), i.Next(t0))

	// 4 days (Sun --> Thurs)
	assert.Equal(t, t0.AddDate(0, 0, 4), i.Next(t0))
}

func TestIntervals(t *testing.T) {
	i := Intervals{Yearly(1), Monthly(1), Weekly(1), OnWeekday{time.Tuesday: true}}
	// Sun, July 4th, 2021 -> Mon, July 4th, 2022
	// Mon, July 4th, 2022 -> Thu, August 4th, 2022
	// Thu, August 4th, 2022 -> Thu, August 11th, 2022
	// Thu, August 11th, 2022 --> Tue,August 16th, 2022
	assert.Equal(t, Sunday_July_4_2021.AddDate(1, 1, 7+5), i.Next(Sunday_July_4_2021))

}

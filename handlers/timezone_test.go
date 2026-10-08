package handlers

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalDaysBounds(t *testing.T) {
	moscow, err := time.LoadLocation("Europe/Moscow")
	require.NoError(t, err)
	newYork, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	berlin, err := time.LoadLocation("Europe/Berlin")
	require.NoError(t, err)

	date := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

	cases := []struct {
		name       string
		from, to   time.Time
		loc        *time.Location
		start, end time.Time
	}{
		{"UTC default", date(2026, 9, 1), date(2026, 9, 30), time.UTC,
			time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)},
		{"UTC+3 month", date(2026, 10, 1), date(2026, 10, 31), moscow,
			time.Date(2026, 9, 30, 21, 0, 0, 0, time.UTC), time.Date(2026, 10, 31, 21, 0, 0, 0, time.UTC)},
		{"UTC-5 year end", date(2026, 12, 31), date(2026, 12, 31), newYork,
			time.Date(2026, 12, 31, 5, 0, 0, 0, time.UTC), time.Date(2027, 1, 1, 5, 0, 0, 0, time.UTC)},
		// Переход на летнее время: сутки 29.03.2026 в Берлине длятся 23 часа.
		{"DST spring forward", date(2026, 3, 29), date(2026, 3, 29), berlin,
			time.Date(2026, 3, 28, 23, 0, 0, 0, time.UTC), time.Date(2026, 3, 29, 22, 0, 0, 0, time.UTC)},
		// Переход на зимнее время: сутки 25.10.2026 в Берлине длятся 25 часов.
		{"DST fall back", date(2026, 10, 25), date(2026, 10, 25), berlin,
			time.Date(2026, 10, 24, 22, 0, 0, 0, time.UTC), time.Date(2026, 10, 25, 23, 0, 0, 0, time.UTC)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start, end := localDaysBounds(tc.from, tc.to, tc.loc)
			assert.True(t, start.Equal(tc.start), "start %s != %s", start.UTC(), tc.start)
			assert.True(t, end.Equal(tc.end), "end %s != %s", end.UTC(), tc.end)
		})
	}
}

func TestLocalDateKey(t *testing.T) {
	moscow, err := time.LoadLocation("Europe/Moscow")
	require.NoError(t, err)
	newYork, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)

	instant := time.Date(2026, 9, 26, 22, 30, 0, 0, time.UTC)
	assert.Equal(t, "2026-09-26", localDateKey(instant, time.UTC))
	assert.Equal(t, "2026-09-27", localDateKey(instant, moscow))
	assert.Equal(t, "2026-09-26", localDateKey(instant, newYork))
	assert.Equal(t, "2026-09-26", localDateKey(time.Date(2026, 9, 27, 1, 0, 0, 0, time.UTC), newYork))
}

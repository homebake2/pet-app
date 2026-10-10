package handlers

import (
	"myauthservice/models"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustParseDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	require.NoError(t, err)
	return d
}

func TestComputeMedicationNextDose_AsNeededIsNil(t *testing.T) {
	start := mustParseDate(t, "2024-01-01")
	now := mustParseDate(t, "2024-06-01")
	got := computeMedicationNextDose(models.MedicationFrequencyAsNeeded, nil, 0, nil, start, nil, now, time.UTC)
	assert.Nil(t, got)
}

func TestComputeMedicationNextDose_FutureStart(t *testing.T) {
	start := mustParseDate(t, "2024-06-10")
	now := mustParseDate(t, "2024-06-01")
	times := []models.MedicationTimeSlot{{Time: "08:00"}, {Time: "20:00"}}

	got := computeMedicationNextDose(models.MedicationFrequencyDaily, nil, 0, times, start, nil, now, time.UTC)

	require.NotNil(t, got)
	assert.Equal(t, "2024-06-10T08:00:00Z", got.UTC().Format(time.RFC3339))
}

func TestComputeMedicationNextDose_TodayLaterTimeSlot(t *testing.T) {
	start := mustParseDate(t, "2024-01-01")
	now := time.Date(2024, 6, 15, 10, 0, 0, 0, time.UTC)
	times := []models.MedicationTimeSlot{{Time: "08:00"}, {Time: "20:00"}}

	got := computeMedicationNextDose(models.MedicationFrequencyDaily, nil, 0, times, start, nil, now, time.UTC)

	require.NotNil(t, got)
	assert.Equal(t, "2024-06-15T20:00:00Z", got.UTC().Format(time.RFC3339))
}

func TestComputeMedicationNextDose_TodayAllPassed(t *testing.T) {
	start := mustParseDate(t, "2024-01-01")
	now := time.Date(2024, 6, 15, 23, 0, 0, 0, time.UTC)
	times := []models.MedicationTimeSlot{{Time: "08:00"}, {Time: "20:00"}}

	got := computeMedicationNextDose(models.MedicationFrequencyDaily, nil, 0, times, start, nil, now, time.UTC)

	require.NotNil(t, got)
	assert.Equal(t, "2024-06-16T08:00:00Z", got.UTC().Format(time.RFC3339))
}

func TestComputeMedicationNextDose_PastEndDate(t *testing.T) {
	start := mustParseDate(t, "2024-01-01")
	end := mustParseDate(t, "2024-01-31")
	now := time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC)
	times := []models.MedicationTimeSlot{{Time: "08:00"}}

	got := computeMedicationNextDose(models.MedicationFrequencyDaily, nil, 0, times, start, &end, now, time.UTC)

	assert.Nil(t, got)
}

func TestComputeMedicationNextDose_EveryNDaysOldStart(t *testing.T) {
	// Курс начался давно, интервал 5 дней — next_dose должен найтись близко
	// к "сегодня", не перебирая всю историю день за днём с нуля.
	start := mustParseDate(t, "2020-01-01")
	now := time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC)
	times := []models.MedicationTimeSlot{{Time: "09:00"}}

	got := computeMedicationNextDose(models.MedicationFrequencyEveryNDays, nil, 5, times, start, nil, now, time.UTC)

	require.NotNil(t, got)
	daysSinceStart := int(got.Sub(start).Hours() / 24)
	assert.Equal(t, 0, daysSinceStart%5)
	assert.False(t, got.Before(now))
}

// times расписания — местное время пояса: в 10:00Z в Москве уже 13:00, и
// приём "12:00" по Москве (09:00Z) прошёл — ближайший "20:00" (17:00Z); в
// Боготе в тот же момент 05:00, ближайший приём — "08:00" местного (13:00Z).
func TestComputeMedicationNextDose_ClientTimeZone(t *testing.T) {
	start := mustParseDate(t, "2024-01-01")
	now := time.Date(2024, 6, 15, 10, 0, 0, 0, time.UTC)
	times := []models.MedicationTimeSlot{{Time: "08:00"}, {Time: "12:00"}, {Time: "20:00"}}

	cases := []struct {
		tz       string
		expected string
	}{
		{"Europe/Moscow", "2024-06-15T17:00:00Z"},
		{"America/Bogota", "2024-06-15T13:00:00Z"},
		{"UTC", "2024-06-15T12:00:00Z"},
	}
	for _, tc := range cases {
		t.Run(tc.tz, func(t *testing.T) {
			loc, err := time.LoadLocation(tc.tz)
			require.NoError(t, err)

			got := computeMedicationNextDose(models.MedicationFrequencyDaily, nil, 0, times, start, nil, now, loc)

			require.NotNil(t, got)
			assert.Equal(t, tc.expected, got.UTC().Format(time.RFC3339))
		})
	}
}

// «Сегодня» берётся по местному времени: в 22:30Z 14 июня в Москве уже
// 01:30 15 июня, поэтому ближайший приём — "08:00" 15-го по Москве (05:00Z
// 15-го), а не пропущенный из-за даты UTC; в Боготе в 02:00Z 15 июня ещё
// 21:00 14-го, и вечерний приём "22:00" 14-го (03:00Z 15-го) ещё впереди.
func TestComputeMedicationNextDose_LocalTodayDiffersFromUTC(t *testing.T) {
	start := mustParseDate(t, "2024-01-01")

	moscow, err := time.LoadLocation("Europe/Moscow")
	require.NoError(t, err)
	got := computeMedicationNextDose(models.MedicationFrequencyDaily, nil, 0, []models.MedicationTimeSlot{{Time: "08:00"}}, start, nil, time.Date(2024, 6, 14, 22, 30, 0, 0, time.UTC), moscow)
	require.NotNil(t, got)
	assert.Equal(t, "2024-06-15T05:00:00Z", got.UTC().Format(time.RFC3339))

	bogota, err := time.LoadLocation("America/Bogota")
	require.NoError(t, err)
	got = computeMedicationNextDose(models.MedicationFrequencyDaily, nil, 0, []models.MedicationTimeSlot{{Time: "22:00"}}, start, nil, time.Date(2024, 6, 15, 2, 0, 0, 0, time.UTC), bogota)
	require.NotNil(t, got)
	assert.Equal(t, "2024-06-15T03:00:00Z", got.UTC().Format(time.RFC3339))
}

// Курс, закончившийся «сегодня» по местному времени, ещё отдаёт вечерний
// приём, даже если по UTC уже наступил следующий день.
func TestComputeMedicationNextDose_EndDateInLocalTime(t *testing.T) {
	start := mustParseDate(t, "2024-06-01")
	end := mustParseDate(t, "2024-06-14")
	bogota, err := time.LoadLocation("America/Bogota")
	require.NoError(t, err)

	got := computeMedicationNextDose(models.MedicationFrequencyDaily, nil, 0, []models.MedicationTimeSlot{{Time: "22:00"}}, start, &end, time.Date(2024, 6, 15, 2, 0, 0, 0, time.UTC), bogota)

	require.NotNil(t, got)
	assert.Equal(t, "2024-06-15T03:00:00Z", got.UTC().Format(time.RFC3339))
}

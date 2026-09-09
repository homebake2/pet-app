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

func TestComputeMedicationScheduleSlots_Daily(t *testing.T) {
	start := mustParseDate(t, "2024-01-01")
	end := mustParseDate(t, "2024-01-03")
	times := []models.MedicationTimeSlot{{Time: "08:00"}, {Time: "20:00"}}

	slots := computeMedicationScheduleSlots(models.MedicationFrequencyDaily, nil, 0, times, start, &end, 60)

	require.Len(t, slots, 6) // 3 дня x 2 времени
	assert.Equal(t, "2024-01-01", slots[0].Date.Format("2006-01-02"))
	assert.Equal(t, "08:00", slots[0].Time)
	assert.Equal(t, "2024-01-03", slots[5].Date.Format("2006-01-02"))
	assert.Equal(t, "20:00", slots[5].Time)
}

func TestComputeMedicationScheduleSlots_SpecificDays(t *testing.T) {
	// 2024-01-01 — понедельник.
	start := mustParseDate(t, "2024-01-01")
	end := mustParseDate(t, "2024-01-14")
	times := []models.MedicationTimeSlot{{Time: "09:00"}}
	weekdays := []int{1, 3} // пн, ср

	slots := computeMedicationScheduleSlots(models.MedicationFrequencySpecificDays, weekdays, 0, times, start, &end, 60)

	require.Len(t, slots, 4) // 2 недели x 2 дня
	for _, s := range slots {
		wd := isoWeekday(s.Date)
		assert.Contains(t, []int{1, 3}, wd)
	}
}

func TestComputeMedicationScheduleSlots_EveryNDays(t *testing.T) {
	start := mustParseDate(t, "2024-01-01")
	end := mustParseDate(t, "2024-01-10")
	times := []models.MedicationTimeSlot{{Time: "07:30"}}

	slots := computeMedicationScheduleSlots(models.MedicationFrequencyEveryNDays, nil, 3, times, start, &end, 60)

	require.Len(t, slots, 4) // Jan 1, 4, 7, 10
	assert.Equal(t, "2024-01-01", slots[0].Date.Format("2006-01-02"))
	assert.Equal(t, "2024-01-04", slots[1].Date.Format("2006-01-02"))
	assert.Equal(t, "2024-01-07", slots[2].Date.Format("2006-01-02"))
	assert.Equal(t, "2024-01-10", slots[3].Date.Format("2006-01-02"))
}

func TestComputeMedicationScheduleSlots_CapAt60(t *testing.T) {
	start := mustParseDate(t, "2024-01-01")
	times := []models.MedicationTimeSlot{{Time: "08:00"}, {Time: "14:00"}, {Time: "20:00"}, {Time: "23:00"}}

	// Без end_date, 4 времени/день -> потолок в 60 достигается на 15-м дне.
	slots := computeMedicationScheduleSlots(models.MedicationFrequencyDaily, nil, 0, times, start, nil, models.MedicationScheduleEventsCap)

	assert.Len(t, slots, 60)
	assert.Equal(t, "2024-01-15", slots[len(slots)-1].Date.Format("2006-01-02"))
}

func TestComputeMedicationScheduleSlots_DoseNoteFallback(t *testing.T) {
	start := mustParseDate(t, "2024-01-01")
	end := start
	note := "2 pipettes"
	times := []models.MedicationTimeSlot{{Time: "08:00", DoseNote: &note}, {Time: "20:00"}}

	slots := computeMedicationScheduleSlots(models.MedicationFrequencyDaily, nil, 0, times, start, &end, 60)

	require.Len(t, slots, 2)
	require.NotNil(t, slots[0].DoseNote)
	assert.Equal(t, "2 pipettes", *slots[0].DoseNote)
	assert.Nil(t, slots[1].DoseNote)
}

func TestComputeMedicationNextDose_AsNeededIsNil(t *testing.T) {
	start := mustParseDate(t, "2024-01-01")
	now := mustParseDate(t, "2024-06-01")
	got := computeMedicationNextDose(models.MedicationFrequencyAsNeeded, nil, 0, nil, start, nil, now)
	assert.Nil(t, got)
}

func TestComputeMedicationNextDose_FutureStart(t *testing.T) {
	start := mustParseDate(t, "2024-06-10")
	now := mustParseDate(t, "2024-06-01")
	times := []models.MedicationTimeSlot{{Time: "08:00"}, {Time: "20:00"}}

	got := computeMedicationNextDose(models.MedicationFrequencyDaily, nil, 0, times, start, nil, now)

	require.NotNil(t, got)
	assert.Equal(t, "2024-06-10T08:00:00Z", got.UTC().Format(time.RFC3339))
}

func TestComputeMedicationNextDose_TodayLaterTimeSlot(t *testing.T) {
	start := mustParseDate(t, "2024-01-01")
	now := time.Date(2024, 6, 15, 10, 0, 0, 0, time.UTC)
	times := []models.MedicationTimeSlot{{Time: "08:00"}, {Time: "20:00"}}

	got := computeMedicationNextDose(models.MedicationFrequencyDaily, nil, 0, times, start, nil, now)

	require.NotNil(t, got)
	assert.Equal(t, "2024-06-15T20:00:00Z", got.UTC().Format(time.RFC3339))
}

func TestComputeMedicationNextDose_TodayAllPassed(t *testing.T) {
	start := mustParseDate(t, "2024-01-01")
	now := time.Date(2024, 6, 15, 23, 0, 0, 0, time.UTC)
	times := []models.MedicationTimeSlot{{Time: "08:00"}, {Time: "20:00"}}

	got := computeMedicationNextDose(models.MedicationFrequencyDaily, nil, 0, times, start, nil, now)

	require.NotNil(t, got)
	assert.Equal(t, "2024-06-16T08:00:00Z", got.UTC().Format(time.RFC3339))
}

func TestComputeMedicationNextDose_PastEndDate(t *testing.T) {
	start := mustParseDate(t, "2024-01-01")
	end := mustParseDate(t, "2024-01-31")
	now := time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC)
	times := []models.MedicationTimeSlot{{Time: "08:00"}}

	got := computeMedicationNextDose(models.MedicationFrequencyDaily, nil, 0, times, start, &end, now)

	assert.Nil(t, got)
}

func TestComputeMedicationNextDose_EveryNDaysOldStart(t *testing.T) {
	// Курс начался давно, интервал 5 дней — next_dose должен найтись близко
	// к "сегодня", не перебирая всю историю день за днём с нуля.
	start := mustParseDate(t, "2020-01-01")
	now := time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC)
	times := []models.MedicationTimeSlot{{Time: "09:00"}}

	got := computeMedicationNextDose(models.MedicationFrequencyEveryNDays, nil, 5, times, start, nil, now)

	require.NotNil(t, got)
	daysSinceStart := int(got.Sub(start).Hours() / 24)
	assert.Equal(t, 0, daysSinceStart%5)
	assert.False(t, got.Before(now))
}

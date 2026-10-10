package handlers

import (
	"myauthservice/models"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func slotsOf(times ...string) []reminderTimeSlot {
	slots := make([]reminderTimeSlot, 0, len(times))
	for _, t := range times {
		slots = append(slots, reminderTimeSlot{Time: t})
	}
	return slots
}

func momentStrings(moments []models.ReminderMoment) []string {
	out := make([]string, 0, len(moments))
	for _, m := range moments {
		out = append(out, m.RemindAt.UTC().Format(time.RFC3339))
	}
	return out
}

func TestComputeReminderMoments_Daily(t *testing.T) {
	start := mustParseDate(t, "2024-01-01")
	end := mustParseDate(t, "2024-01-03")
	now := time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC)
	spec := reminderScheduleSpec{FrequencyType: "daily", Times: slotsOf("08:00", "20:00"), StartDate: start, EndDate: &end}

	moments := computeReminderMoments(spec, time.UTC, now, nil, 60)

	assert.Equal(t, []string{
		"2024-01-01T08:00:00Z", "2024-01-01T20:00:00Z",
		"2024-01-02T08:00:00Z", "2024-01-02T20:00:00Z",
		"2024-01-03T08:00:00Z", "2024-01-03T20:00:00Z",
	}, momentStrings(moments))
}

// Прошедшие моменты расписания не материализуются, даже если start_date в
// прошлом; момент «сейчас» тоже прошедший (строго позже now).
func TestComputeReminderMoments_OnlyFutureMoments(t *testing.T) {
	start := mustParseDate(t, "2024-01-01")
	end := mustParseDate(t, "2024-01-03")
	now := time.Date(2024, 1, 2, 8, 0, 0, 0, time.UTC)
	spec := reminderScheduleSpec{FrequencyType: "daily", Times: slotsOf("08:00", "20:00"), StartDate: start, EndDate: &end}

	moments := computeReminderMoments(spec, time.UTC, now, nil, 60)

	assert.Equal(t, []string{
		"2024-01-02T20:00:00Z",
		"2024-01-03T08:00:00Z", "2024-01-03T20:00:00Z",
	}, momentStrings(moments))
}

func TestComputeReminderMoments_SpecificDays(t *testing.T) {
	// 2024-01-01 — понедельник.
	start := mustParseDate(t, "2024-01-01")
	end := mustParseDate(t, "2024-01-14")
	now := time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC)
	spec := reminderScheduleSpec{FrequencyType: "specific_days", Weekdays: []int{1, 3}, Times: slotsOf("09:00"), StartDate: start, EndDate: &end}

	moments := computeReminderMoments(spec, time.UTC, now, nil, 60)

	require.Len(t, moments, 4)
	for _, m := range moments {
		assert.Contains(t, []int{1, 3}, isoWeekday(m.RemindAt))
	}
}

// Отсчёт интервала идёт от start_date, в том числе когда она в прошлом.
func TestComputeReminderMoments_EveryNDaysCountsFromPastStart(t *testing.T) {
	start := mustParseDate(t, "2024-01-01")
	now := time.Date(2024, 1, 10, 12, 0, 0, 0, time.UTC)
	spec := reminderScheduleSpec{FrequencyType: "every_n_days", IntervalDays: 3, Times: slotsOf("07:30"), StartDate: start}

	moments := computeReminderMoments(spec, time.UTC, now, nil, 3)

	// 1, 4, 7, 10 января (10-е в 07:30 уже прошло), затем 13, 16, 19.
	assert.Equal(t, []string{"2024-01-13T07:30:00Z", "2024-01-16T07:30:00Z", "2024-01-19T07:30:00Z"}, momentStrings(moments))
}

func TestComputeReminderMoments_CapAt60(t *testing.T) {
	start := mustParseDate(t, "2024-01-01")
	now := time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC)
	spec := reminderScheduleSpec{FrequencyType: "daily", Times: slotsOf("08:00", "14:00", "20:00", "23:00"), StartDate: start}

	moments := computeReminderMoments(spec, time.UTC, now, nil, models.ReminderMaxMomentsPerOperation)

	require.Len(t, moments, 60)
	// 4 времени в день: потолок достигается на 15-м дне.
	assert.Equal(t, "2024-01-15T23:00:00Z", moments[59].RemindAt.UTC().Format(time.RFC3339))
}

// Закрытые моменты тех же настроек пропускаются независимо от причины
// закрытия.
func TestComputeReminderMoments_SkipsClosedMoments(t *testing.T) {
	start := mustParseDate(t, "2024-01-01")
	end := mustParseDate(t, "2024-01-02")
	now := time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC)
	spec := reminderScheduleSpec{FrequencyType: "daily", Times: slotsOf("08:00"), StartDate: start, EndDate: &end}
	closed := map[int64]bool{time.Date(2024, 1, 1, 8, 0, 0, 0, time.UTC).Unix(): true}

	moments := computeReminderMoments(spec, time.UTC, now, closed, 60)

	assert.Equal(t, []string{"2024-01-02T08:00:00Z"}, momentStrings(moments))
}

func TestComputeReminderMoments_Once(t *testing.T) {
	start := mustParseDate(t, "2024-06-10")
	now := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	spec := reminderScheduleSpec{FrequencyType: "once", Times: slotsOf("10:15"), StartDate: start}

	assert.Equal(t, []string{"2024-06-10T10:15:00Z"}, momentStrings(computeReminderMoments(spec, time.UTC, now, nil, 60)))

	// Момент в прошлом — расписание пустое.
	later := time.Date(2024, 6, 10, 10, 15, 0, 0, time.UTC)
	assert.Empty(t, computeReminderMoments(spec, time.UTC, later, nil, 60))
}

// Слоты несут собственную заметку напоминания (доза по времени приёма).
func TestComputeReminderMoments_SlotNotes(t *testing.T) {
	start := mustParseDate(t, "2024-01-01")
	end := start
	now := time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC)
	note := "2 пипетки"
	spec := reminderScheduleSpec{
		FrequencyType: "daily", StartDate: start, EndDate: &end,
		Times: []reminderTimeSlot{{Time: "08:00", Notes: &note}, {Time: "20:00"}},
	}

	moments := computeReminderMoments(spec, time.UTC, now, nil, 60)

	require.Len(t, moments, 2)
	require.NotNil(t, moments[0].Notes)
	assert.Equal(t, "2 пипетки", *moments[0].Notes)
	assert.Nil(t, moments[1].Notes)
}

// Местное время в поясе расписания: 09:00 по Москве — 06:00Z.
func TestComputeReminderMoments_ClientTimeZone(t *testing.T) {
	moscow, err := time.LoadLocation("Europe/Moscow")
	require.NoError(t, err)
	start := mustParseDate(t, "2024-06-10")
	now := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	spec := reminderScheduleSpec{FrequencyType: "once", Times: slotsOf("09:00"), StartDate: start}

	assert.Equal(t, []string{"2024-06-10T06:00:00Z"}, momentStrings(computeReminderMoments(spec, moscow, now, nil, 60)))
}

// Переход на летнее время вперёд (Нью-Йорк, 10 марта 2024: 02:00 -> 03:00):
// несуществующее 02:30 сдвигается на длину пропуска — 03:30 EDT (07:30Z).
func TestLocalMoment_DSTGapShiftsForwardByGapLength(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)

	got := localMoment(mustParseDate(t, "2024-03-10"), "02:30", ny)

	assert.Equal(t, "2024-03-10T07:30:00Z", got.UTC().Format(time.RFC3339))
	assert.Equal(t, "03:30", got.In(ny).Format("15:04"))
}

// Переход назад (Нью-Йорк, 3 ноября 2024: 02:00 EDT -> 01:00 EST): 01:30
// встречается дважды, берётся первое вхождение — 01:30 EDT (05:30Z).
func TestLocalMoment_DSTOverlapTakesFirstOccurrence(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)

	got := localMoment(mustParseDate(t, "2024-11-03"), "01:30", ny)

	assert.Equal(t, "2024-11-03T05:30:00Z", got.UTC().Format(time.RFC3339))
}

// Обычное время вне переходов не меняется.
func TestLocalMoment_RegularTime(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)

	got := localMoment(mustParseDate(t, "2024-07-01"), "12:00", ny)

	assert.Equal(t, "2024-07-01T16:00:00Z", got.UTC().Format(time.RFC3339))
}

func TestParseReminderSchedule_Validation(t *testing.T) {
	interval := 3
	cases := []struct {
		name    string
		in      reminderScheduleInput
		wantErr bool
	}{
		{"daily ok", reminderScheduleInput{FrequencyType: "daily", Times: []string{"08:00"}, StartDate: "2024-01-01"}, false},
		{"times с секундами", reminderScheduleInput{FrequencyType: "daily", Times: []string{"08:00:00"}, StartDate: "2024-01-01"}, false},
		{"once с end_date", reminderScheduleInput{FrequencyType: "once", Times: []string{"08:00"}, StartDate: "2024-01-01", EndDate: strPtr("2024-01-05")}, true},
		{"once с двумя временами", reminderScheduleInput{FrequencyType: "once", Times: []string{"08:00", "09:00"}, StartDate: "2024-01-01"}, true},
		{"specific_days без weekdays", reminderScheduleInput{FrequencyType: "specific_days", Times: []string{"08:00"}, StartDate: "2024-01-01"}, true},
		{"daily с weekdays", reminderScheduleInput{FrequencyType: "daily", Weekdays: []int{1}, Times: []string{"08:00"}, StartDate: "2024-01-01"}, true},
		{"every_n_days без interval", reminderScheduleInput{FrequencyType: "every_n_days", Times: []string{"08:00"}, StartDate: "2024-01-01"}, true},
		{"every_n_days ok", reminderScheduleInput{FrequencyType: "every_n_days", IntervalDays: &interval, Times: []string{"08:00"}, StartDate: "2024-01-01"}, false},
		{"daily с interval", reminderScheduleInput{FrequencyType: "daily", IntervalDays: &interval, Times: []string{"08:00"}, StartDate: "2024-01-01"}, true},
		{"пять времён", reminderScheduleInput{FrequencyType: "daily", Times: []string{"01:00", "02:00", "03:00", "04:00", "05:00"}, StartDate: "2024-01-01"}, true},
		{"повтор времени", reminderScheduleInput{FrequencyType: "daily", Times: []string{"08:00", "08:00:00"}, StartDate: "2024-01-01"}, true},
		{"end_date раньше start_date", reminderScheduleInput{FrequencyType: "daily", Times: []string{"08:00"}, StartDate: "2024-01-05", EndDate: strPtr("2024-01-01")}, true},
		{"некорректное время", reminderScheduleInput{FrequencyType: "daily", Times: []string{"25:00"}, StartDate: "2024-01-01"}, true},
		{"неизвестная частота", reminderScheduleInput{FrequencyType: "as_needed", Times: []string{"08:00"}, StartDate: "2024-01-01"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, msg := parseReminderSchedule(c.in)
			if c.wantErr {
				assert.NotEmpty(t, msg)
			} else {
				assert.Empty(t, msg)
			}
		})
	}
}

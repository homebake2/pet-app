// Package handlers: вычисление next_dose лекарства — ближайшего будущего
// приёма по расписанию лекарства. Сами напоминания набора материализует
// единый расчёт расписания напоминаний (см. reminder_schedule.go).
package handlers

import (
	"myauthservice/models"
	"time"
)

// isoWeekday возвращает ISO-номер дня недели (пн=1..вс=7) — Go's time.Weekday
// нумерует вс=0..сб=6.
func isoWeekday(d time.Time) int {
	wd := int(d.Weekday())
	if wd == 0 {
		return 7
	}
	return wd
}

func containsInt(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// medicationDateMatches проверяет, входит ли календарная дата d в
// расписание паттерна frequencyType. Не вызывается для
// frequencyType=as_needed — там расписания нет.
func medicationDateMatches(frequencyType string, weekdays []int, intervalDays int, startDate, d time.Time) bool {
	switch frequencyType {
	case models.MedicationFrequencyDaily:
		return true
	case models.MedicationFrequencySpecificDays:
		return containsInt(weekdays, isoWeekday(d))
	case models.MedicationFrequencyEveryNDays:
		if intervalDays <= 0 {
			return false
		}
		days := int(d.Sub(startDate).Hours() / 24)
		return days >= 0 && days%intervalDays == 0
	default:
		return false
	}
}

// medicationScheduleDates перебирает календарные даты начиная со startDate
// (включительно) по возрастанию, вызывая yield для каждой даты, входящей в
// расписание, пока yield не вернёт false, либо не будет достигнута endDate,
// либо не будет исчерпан maxDays (предохранитель от бесконечного перебора).
func medicationScheduleDates(frequencyType string, weekdays []int, intervalDays int, startDate time.Time, endDate *time.Time, maxDays int, yield func(time.Time) bool) {
	for i := 0; i < maxDays; i++ {
		d := startDate.AddDate(0, 0, i)
		if endDate != nil && d.After(*endDate) {
			return
		}
		if medicationDateMatches(frequencyType, weekdays, intervalDays, startDate, d) {
			if !yield(d) {
				return
			}
		}
	}
}

// medicationNextDoseLookaheadDays — верхняя граница перебора для
// вычисления next_dose. В отличие от материализации напоминаний,
// next_dose не ограничен потолком в 60 напоминаний и должен находить ближайший
// будущий приём независимо от того, как давно начался курс — поэтому
// граница шире (~10 лет), но всё ещё конечна, чтобы не зациклиться на
// некорректных входных данных.
const medicationNextDoseLookaheadDays = models.MedicationScheduleMaxLookaheadDays * 5

// computeMedicationNextDose вычисляет минимальный момент (дата+время)
// расписания, который >= now, по текущим полям расписания курса. Даты и
// times расписания — местное время пояса loc: «сегодня» берётся по now в
// этом поясе, и каждый приём строится как момент местного времени. nil, если
// frequency_type=as_needed, либо весь рассчитанный график раньше now
// (актуально для курсов с end_date в прошлом) — см. "Вычисление next_dose".
func computeMedicationNextDose(frequencyType string, weekdays []int, intervalDays int, times []models.MedicationTimeSlot, startDate time.Time, endDate *time.Time, now time.Time, loc *time.Location) *time.Time {
	if frequencyType == models.MedicationFrequencyAsNeeded || len(times) == 0 {
		return nil
	}

	// Календарные даты расписания хранятся как полночь UTC (см.
	// parseDateOnly), поэтому и «сегодня» пояса loc приводится к такому же
	// виду — иначе сравнение с startDate сдвигалось бы на смещение пояса.
	localNow := now.In(loc)
	today := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, time.UTC)
	from := startDate
	if today.After(from) {
		if frequencyType == models.MedicationFrequencyEveryNDays && intervalDays > 0 {
			daysSinceStart := int(today.Sub(startDate).Hours() / 24)
			steps := daysSinceStart / intervalDays
			from = startDate.AddDate(0, 0, steps*intervalDays)
		} else {
			from = today
		}
	}

	var result *time.Time
	medicationScheduleDates(frequencyType, weekdays, intervalDays, from, endDate, medicationNextDoseLookaheadDays, func(d time.Time) bool {
		for _, t := range times {
			at := localMoment(d, t.Time, loc)
			if !at.Before(now) {
				result = &at
				return false
			}
		}
		return true
	})
	return result
}

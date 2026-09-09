// Package handlers: расчёт расписания приёма лекарства — общая функция для
// создания (add_event=true), ручного «Добавить событие»
// (POST /medications/{id}/events), регенерации (PATCH .../{id} с
// regenerate_events=true) и вычисления next_dose. См. "Лекарства —
// Backend", раздел «Расчёт расписания».
package handlers

import (
	"myauthservice/models"
	"strconv"
	"strings"
	"time"
)

// medicationScheduleSlot — один рассчитанный момент приёма препарата: дата +
// элемент times, из которого при материализации строится notes/событие.
type medicationScheduleSlot struct {
	Date     time.Time
	Time     string
	DoseNote *string
}

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
// расписание паттерна frequencyType (см. "Расчёт расписания", шаг 1).
// Не вызывается для frequencyType=as_needed — там расписания нет.
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
// либо не будет исчерпан maxDays (предохранитель от бесконечного перебора,
// см. models.MedicationScheduleMaxLookaheadDays).
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

// computeMedicationScheduleSlots материализует расписание для (пере-)создания
// событий: не более eventsCap слотов (дата×времена, считая по всем
// времени-слотам сразу — см. "Потолок числа генерируемых событий"), перебор
// дат ограничен models.MedicationScheduleMaxLookaheadDays. Не вызывается для
// frequency_type=as_needed.
func computeMedicationScheduleSlots(frequencyType string, weekdays []int, intervalDays int, times []models.MedicationTimeSlot, startDate time.Time, endDate *time.Time, eventsCap int) []medicationScheduleSlot {
	slots := make([]medicationScheduleSlot, 0, eventsCap)
	medicationScheduleDates(frequencyType, weekdays, intervalDays, startDate, endDate, models.MedicationScheduleMaxLookaheadDays, func(d time.Time) bool {
		for _, t := range times {
			if len(slots) >= eventsCap {
				return false
			}
			slots = append(slots, medicationScheduleSlot{Date: d, Time: t.Time, DoseNote: t.DoseNote})
		}
		return len(slots) < eventsCap
	})
	return slots
}

// medicationNextDoseLookaheadDays — верхняя граница перебора для
// вычисления next_dose. В отличие от материализации событий (см. выше),
// next_dose не ограничен потолком в 60 событий и должен находить ближайший
// будущий приём независимо от того, как давно начался курс — поэтому
// граница шире (~10 лет), но всё ещё конечна, чтобы не зациклиться на
// некорректных входных данных.
const medicationNextDoseLookaheadDays = models.MedicationScheduleMaxLookaheadDays * 5

// computeMedicationNextDose вычисляет минимальный момент (дата+время)
// расписания, который >= now, по текущим полям расписания курса. nil, если
// frequency_type=as_needed, либо весь рассчитанный график раньше now
// (актуально для курсов с end_date в прошлом) — см. "Вычисление next_dose".
func computeMedicationNextDose(frequencyType string, weekdays []int, intervalDays int, times []models.MedicationTimeSlot, startDate time.Time, endDate *time.Time, now time.Time) *time.Time {
	if frequencyType == models.MedicationFrequencyAsNeeded || len(times) == 0 {
		return nil
	}

	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
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
			at := parseMedicationDateTime(d, t.Time)
			if !at.Before(now) {
				result = &at
				return false
			}
		}
		return true
	})
	return result
}

// parseMedicationDateTime строит time.Time (UTC) из даты и "HH:mm"/"HH:mm:ss".
func parseMedicationDateTime(d time.Time, timeOfDay string) time.Time {
	hh, mm, ss := 0, 0, 0
	parts := strings.Split(timeOfDay, ":")
	if len(parts) >= 2 {
		hh, _ = strconv.Atoi(parts[0])
		mm, _ = strconv.Atoi(parts[1])
	}
	if len(parts) == 3 {
		ss, _ = strconv.Atoi(parts[2])
	}
	return time.Date(d.Year(), d.Month(), d.Day(), hh, mm, ss, 0, time.UTC)
}

// Единый расчёт расписания напоминаний для настроек любого источника: им
// пользуются создание настроек, изменение расписания, набор напоминаний
// лекарства (создание, пересоздание, ручное «Добавить напоминания») и
// напоминание на дату следующей вакцинации.
package handlers

import (
	"myauthservice/models"
	"sort"
	"strconv"
	"strings"
	"time"
)

// reminderTimeSlot — время суток расписания с необязательной собственной
// заметкой напоминания (доза по времени приёма лекарства).
type reminderTimeSlot struct {
	// Time — нормализованное время 'HH:mm' (либо 'HH:mm:ss', если секунды
	// не нулевые).
	Time  string
	Notes *string
}

// reminderScheduleSpec — расписание настроек напоминания.
type reminderScheduleSpec struct {
	FrequencyType string
	Weekdays      []int
	IntervalDays  int
	Times         []reminderTimeSlot
	// StartDate/EndDate — календарные даты (полночь UTC; смысл у них — только
	// год/месяц/день в часовом поясе расписания).
	StartDate time.Time
	EndDate   *time.Time
}

// normalizeTimeOfDay приводит допустимое значение 'HH:mm' либо 'HH:mm:ss' к
// каноническому виду: 'HH:mm', если секунды нулевые, иначе 'HH:mm:ss'.
// Значения '09:00' и '09:00:00' — одно и то же время суток.
func normalizeTimeOfDay(s string) string {
	parts := strings.Split(s, ":")
	if len(parts) == 3 && parts[2] != "00" {
		return s
	}
	return parts[0] + ":" + parts[1]
}

// parseTimeOfDay разбирает 'HH:mm[:ss]'.
func parseTimeOfDay(s string) (hh, mm, ss int) {
	parts := strings.Split(s, ":")
	if len(parts) >= 2 {
		hh, _ = strconv.Atoi(parts[0])
		mm, _ = strconv.Atoi(parts[1])
	}
	if len(parts) == 3 {
		ss, _ = strconv.Atoi(parts[2])
	}
	return hh, mm, ss
}

// localMoment строит момент времени из календарной даты d и времени суток
// как местное время пояса loc. Если такого местного времени не существует
// (переход на летнее время вперёд), момент сдвигается на длину пропуска; если
// оно встречается дважды (переход назад), берётся первое вхождение.
func localMoment(d time.Time, timeOfDay string, loc *time.Location) time.Time {
	hh, mm, ss := parseTimeOfDay(timeOfDay)
	// Местное время как «настенные часы» в UTC: по нему ищутся смещения пояса
	// вокруг момента — до и после ближайшего перехода.
	wall := time.Date(d.Year(), d.Month(), d.Day(), hh, mm, ss, 0, time.UTC)
	_, offBefore := wall.Add(-24 * time.Hour).In(loc).Zone()
	_, offAfter := wall.Add(24 * time.Hour).In(loc).Zone()

	var best time.Time
	found := false
	for _, off := range []int{offBefore, offAfter} {
		candidate := wall.Add(-time.Duration(off) * time.Second)
		local := candidate.In(loc)
		if local.Year() != d.Year() || local.Month() != d.Month() || local.Day() != d.Day() ||
			local.Hour() != hh || local.Minute() != mm || local.Second() != ss {
			continue
		}
		if !found || candidate.Before(best) {
			best = candidate
			found = true
		}
	}
	if found {
		return best
	}
	// Пропуск при переходе вперёд: смещение до перехода сдвигает момент на
	// длину пропуска вперёд.
	return wall.Add(-time.Duration(offBefore) * time.Second)
}

// reminderDateMatches проверяет, входит ли календарная дата d в расписание.
func reminderDateMatches(spec reminderScheduleSpec, d time.Time) bool {
	switch spec.FrequencyType {
	case models.ReminderFrequencyOnce:
		return d.Equal(spec.StartDate)
	case models.ReminderFrequencyDaily:
		return true
	case models.ReminderFrequencySpecificDays:
		return containsInt(spec.Weekdays, isoWeekday(d))
	case models.ReminderFrequencyEveryNDays:
		if spec.IntervalDays <= 0 {
			return false
		}
		days := int(d.Sub(spec.StartDate).Hours() / 24)
		return days >= 0 && days%spec.IntervalDays == 0
	default:
		return false
	}
}

// computeReminderMoments рассчитывает моменты расписания: только строго
// позже now, без моментов, закрытых у тех же настроек (closed — множество
// unix-секунд), не больше maxMoments (потолок считается по всем временам
// сразу; остаток расписания не материализуется). loc — часовой пояс, в
// котором трактуются даты и времена. Времена внутри дня идут по возрастанию,
// чтобы потолок обрезал расписание с конца, а не в середине дня.
func computeReminderMoments(spec reminderScheduleSpec, loc *time.Location, now time.Time, closed map[int64]bool, maxMoments int) []models.ReminderMoment {
	slots := append([]reminderTimeSlot(nil), spec.Times...)
	sort.SliceStable(slots, func(i, j int) bool { return slots[i].Time < slots[j].Time })

	// Перебор начинается со startDate, но даты заведомо до «вчера» в поясе
	// расписания моментов в будущем не дают, поэтому перебор пропускает их:
	// иначе расписание, начатое годы назад, перебиралось бы с самого начала.
	first := spec.StartDate
	localNow := now.In(loc)
	yesterday := time.Date(localNow.Year(), localNow.Month(), localNow.Day()-1, 0, 0, 0, 0, time.UTC)
	if yesterday.After(first) {
		if spec.FrequencyType == models.ReminderFrequencyEveryNDays && spec.IntervalDays > 0 {
			days := int(yesterday.Sub(spec.StartDate).Hours() / 24)
			first = spec.StartDate.AddDate(0, 0, (days/spec.IntervalDays)*spec.IntervalDays)
		} else {
			first = yesterday
		}
	}

	moments := make([]models.ReminderMoment, 0, maxMoments)
	seen := make(map[int64]bool)
	if spec.FrequencyType == models.ReminderFrequencyOnce {
		// Разовое расписание — единственная дата start_date; перебор не нужен.
		for _, slot := range slots {
			at := localMoment(spec.StartDate, slot.Time, loc)
			if at.After(now) && !closed[at.Unix()] && len(moments) < maxMoments {
				moments = append(moments, models.ReminderMoment{RemindAt: at.UTC(), Notes: slot.Notes})
			}
		}
		return moments
	}
	matched := false
	// Верхняя граница числа итераций — защита от бесконечного перебора;
	// заведомо больше, чем нужно для 60 моментов при любом допустимом
	// расписании (самый редкий — одно время раз в 365 дней).
	maxIterations := (maxMoments + 1) * 366
	for i := 0; i < maxIterations; i++ {
		d := first.AddDate(0, 0, i)
		if spec.EndDate != nil && d.After(*spec.EndDate) {
			break
		}
		if reminderDateMatches(spec, d) {
			matched = true
			for _, slot := range slots {
				at := localMoment(d, slot.Time, loc)
				if !at.After(now) {
					continue
				}
				unix := at.Unix()
				if closed[unix] || seen[unix] {
					continue
				}
				seen[unix] = true
				moments = append(moments, models.ReminderMoment{RemindAt: at.UTC(), Notes: slot.Notes})
				if len(moments) >= maxMoments {
					return moments
				}
			}
		} else if spec.EndDate == nil && !matched && int(d.Sub(spec.StartDate).Hours()/24) >= models.ReminderScheduleNoMatchLookaheadDays {
			break
		}
	}
	return moments
}

// reminderScheduleInput — поля расписания из запроса (создание настроек,
// изменение расписания, импорт): строковые даты и времена до разбора.
type reminderScheduleInput struct {
	FrequencyType string
	Weekdays      []int
	IntervalDays  *int
	Times         []string
	StartDate     string
	EndDate       *string
}

// parseReminderSchedule проверяет согласованность полей расписания с
// frequency_type и разбирает их. Возвращает сообщение об ошибке для ответа
// 400 (пустая строка — расписание корректно). Времена нормализуются; слоты
// не несут собственных заметок — их при необходимости проставляет вызывающий
// код.
func parseReminderSchedule(in reminderScheduleInput) (reminderScheduleSpec, string) {
	var spec reminderScheduleSpec
	if !models.IsValidReminderFrequencyType(in.FrequencyType) {
		return spec, "Некорректное значение frequency_type"
	}
	spec.FrequencyType = in.FrequencyType

	if in.FrequencyType == models.ReminderFrequencySpecificDays {
		if len(in.Weekdays) < 1 || len(in.Weekdays) > 7 {
			return spec, "Поле weekdays обязательно и должно содержать 1-7 уникальных значений 1-7 при frequency_type=specific_days"
		}
		seen := map[int]bool{}
		for _, d := range in.Weekdays {
			if d < models.MedicationMinWeekday || d > models.MedicationMaxWeekday || seen[d] {
				return spec, "Поле weekdays должно содержать уникальные значения в диапазоне 1-7"
			}
			seen[d] = true
		}
		spec.Weekdays = in.Weekdays
	} else if in.Weekdays != nil {
		return spec, "Поле weekdays допустимо только при frequency_type=specific_days"
	}

	if in.FrequencyType == models.ReminderFrequencyEveryNDays {
		if in.IntervalDays == nil || *in.IntervalDays < models.MedicationMinIntervalDays || *in.IntervalDays > models.MedicationMaxIntervalDays {
			return spec, "Поле interval_days обязательно и должно быть в диапазоне 2-365 при frequency_type=every_n_days"
		}
		spec.IntervalDays = *in.IntervalDays
	} else if in.IntervalDays != nil {
		return spec, "Поле interval_days допустимо только при frequency_type=every_n_days"
	}

	if len(in.Times) < 1 || len(in.Times) > models.ReminderMaxTimesCount {
		return spec, "Поле times должно содержать 1-4 значения HH:mm"
	}
	if in.FrequencyType == models.ReminderFrequencyOnce && len(in.Times) != 1 {
		return spec, "Поле times должно содержать ровно одно значение при frequency_type=once"
	}
	seenTimes := map[string]bool{}
	for _, t := range in.Times {
		if !isValidTimeOfDay(t) {
			return spec, "Поле times содержит значение не в формате HH:mm"
		}
		normalized := normalizeTimeOfDay(t)
		if seenTimes[normalized] {
			return spec, "Поле times содержит повторяющееся время"
		}
		seenTimes[normalized] = true
		spec.Times = append(spec.Times, reminderTimeSlot{Time: normalized})
	}

	startDate, err := parseDateOnly(in.StartDate)
	if err != nil {
		return spec, "Некорректный формат start_date, ожидается YYYY-MM-DD"
	}
	spec.StartDate = startDate

	if in.EndDate != nil {
		if in.FrequencyType == models.ReminderFrequencyOnce {
			return spec, "Поле end_date недопустимо при frequency_type=once"
		}
		endDate, err := parseDateOnly(*in.EndDate)
		if err != nil {
			return spec, "Некорректный формат end_date, ожидается YYYY-MM-DD"
		}
		if endDate.Before(startDate) {
			return spec, "Поле end_date не может быть раньше start_date"
		}
		spec.EndDate = &endDate
	}
	return spec, ""
}

// reminderPlanTimes возвращает нормализованные времена расписания для
// хранения в reminder_plan.times.
func reminderPlanTimes(spec reminderScheduleSpec) []string {
	times := make([]string, 0, len(spec.Times))
	for _, slot := range spec.Times {
		times = append(times, slot.Time)
	}
	return times
}

// sameReminderSchedule сообщает, совпадает ли расписание spec с сохранённым в
// настройках (поля расписания и часовой пояс не сравниваются по вспомогательным
// представлениям — только по значениям, значимым для расчёта моментов).
func sameReminderSchedule(spec reminderScheduleSpec, plan models.ReminderPlanDB, tz string) bool {
	if spec.FrequencyType != plan.FrequencyType || tz != plan.TZ {
		return false
	}
	if !equalIntSlices(sortedCopy(spec.Weekdays), sortedCopy(plan.Weekdays)) {
		return false
	}
	planInterval := 0
	if plan.IntervalDays.Valid {
		planInterval = int(plan.IntervalDays.Int64)
	}
	if spec.IntervalDays != planInterval {
		return false
	}
	if !spec.StartDate.Equal(plan.StartDate) {
		return false
	}
	if (spec.EndDate == nil) != !plan.EndDate.Valid {
		return false
	}
	if spec.EndDate != nil && !spec.EndDate.Equal(plan.EndDate.Time) {
		return false
	}
	specTimes := reminderPlanTimes(spec)
	planTimes := make([]string, 0, len(plan.Times))
	for _, t := range plan.Times {
		planTimes = append(planTimes, normalizeTimeOfDay(t))
	}
	sort.Strings(specTimes)
	sort.Strings(planTimes)
	return strings.Join(specTimes, ",") == strings.Join(planTimes, ",")
}

func sortedCopy(xs []int) []int {
	out := append([]int(nil), xs...)
	sort.Ints(out)
	return out
}

// reminderScheduleSpecFromPlan восстанавливает расписание из сохранённых
// настроек (слоты без заметок).
func reminderScheduleSpecFromPlan(plan models.ReminderPlanDB) reminderScheduleSpec {
	spec := reminderScheduleSpec{
		FrequencyType: plan.FrequencyType,
		Weekdays:      plan.Weekdays,
		StartDate:     plan.StartDate,
	}
	if plan.IntervalDays.Valid {
		spec.IntervalDays = int(plan.IntervalDays.Int64)
	}
	if plan.EndDate.Valid {
		end := plan.EndDate.Time
		spec.EndDate = &end
	}
	for _, t := range plan.Times {
		spec.Times = append(spec.Times, reminderTimeSlot{Time: normalizeTimeOfDay(t)})
	}
	return spec
}

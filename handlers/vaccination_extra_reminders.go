package handlers

import (
	"database/sql"
	"net/http"
	"sort"
	"time"

	"myauthservice/database"
	"myauthservice/models"
	"myauthservice/openapi"

	"github.com/google/uuid"
)

// Дополнительные напоминания вакцинации — см. «Вакцинации — Backend»: это
// обычные настройки напоминания (source = vaccination_extra, source_id = id
// вакцинации, разовое расписание, одно напоминание); напоминание на
// следующую вакцинацию — source = vaccination, id = vaccination.next_plan_id.
// Отдельной таблицы для дополнительных нет.

func vaccinationBadRequest(msg string) error {
	return newReminderHTTPError(http.StatusBadRequest, openapi.VALIDATIONERROR, msg)
}

// validateVaccinationExtraReminderItems проверяет формат элементов
// extra_reminders: не более пяти, дата YYYY-MM-DD и время HH:MM[:SS]
// обязательны, plan_id (если разрешён) — UUID и не повторяется.
func validateVaccinationExtraReminderItems(items []models.VaccinationExtraReminderRequest, allowPlanID bool) string {
	if len(items) > models.VaccinationExtraRemindersMax {
		return "Дополнительных напоминаний вакцинации не может быть больше пяти"
	}
	seenPlans := make(map[string]bool, len(items))
	for _, item := range items {
		if !isValidDateOnly(item.Date) {
			return "Некорректный формат date дополнительного напоминания, ожидается YYYY-MM-DD"
		}
		if item.Time == "" || !isValidTimeOfDay(item.Time) {
			return "Некорректный формат time дополнительного напоминания, ожидается HH:MM[:SS]"
		}
		if item.PlanID == nil || *item.PlanID == "" {
			continue
		}
		if !allowPlanID {
			return "Поле plan_id дополнительного напоминания не допускается при создании"
		}
		if _, err := uuid.Parse(*item.PlanID); err != nil {
			return "Поле plan_id дополнительного напоминания должно быть UUID"
		}
		if seenPlans[*item.PlanID] {
			return "plan_id дополнительных напоминаний не должны повторяться"
		}
		seenPlans[*item.PlanID] = true
	}
	return ""
}

// vaccinationExtraMoment — итоговое дополнительное напоминание для проверки
// правил момента. fresh — оно создаётся либо переносится этим запросом.
type vaccinationExtraMoment struct {
	moment time.Time
	fresh  bool
}

// validateVaccinationExtraMoments проверяет итоговый набор дополнительных
// напоминаний: создаваемые и переносимые — строго в будущем; создаваемые и
// переносимые (а при изменении момента следующей вакцинации — все) — не
// позже момента следующей вакцинации; моменты всех напоминаний вакцинации
// различны (напоминание на следующую дату учитывается, если оно есть).
func validateVaccinationExtraMoments(extras []vaccinationExtraMoment, nextMoment time.Time, nextChanged, hasMain bool, now time.Time) error {
	seen := make(map[int64]bool, len(extras)+1)
	if hasMain {
		seen[nextMoment.Unix()] = true
	}
	for _, e := range extras {
		if e.fresh && !e.moment.After(now) {
			return vaccinationBadRequest("Момент дополнительного напоминания должен быть в будущем")
		}
		if (e.fresh || nextChanged) && e.moment.After(nextMoment) {
			return vaccinationBadRequest("Момент дополнительного напоминания не может быть позже даты и времени следующей вакцинации")
		}
		key := e.moment.Unix()
		if seen[key] {
			return vaccinationBadRequest("Моменты напоминаний вакцинации не должны совпадать")
		}
		seen[key] = true
	}
	return nil
}

// extraPlanTimeOfDay возвращает время суток настроек дополнительного
// напоминания.
func extraPlanTimeOfDay(plan models.ReminderPlanDB) string {
	if len(plan.Times) == 0 {
		return "00:00"
	}
	return normalizeTimeOfDay(plan.Times[0])
}

// extraPlanMatches сообщает, что разовое расписание настроек уже совпадает с
// переданными датой и временем суток.
func extraPlanMatches(plan models.ReminderPlanDB, date time.Time, timeOfDay string) bool {
	return plan.StartDate.Format("2006-01-02") == date.Format("2006-01-02") &&
		extraPlanTimeOfDay(plan) == normalizeTimeOfDay(timeOfDay)
}

// createVaccinationExtraReminders создаёт настройки дополнительных
// напоминаний (по одному напоминанию в каждых) типа vaccination со значением
// {name, vaccination_at}, где vaccination_at — момент следующей вакцинации
// (nextMoment). Моменты проверены вызывающим кодом
// (validateVaccinationExtraMoments).
func createVaccinationExtraReminders(exec database.Executor, userID string, petID, vaccinationID uuid.UUID, items []models.VaccinationExtraReminderRequest, loc *time.Location, tz, name string, nextMoment time.Time, now time.Time) error {
	for _, item := range items {
		date, _ := parseDateOnly(item.Date)
		timeOfDay := item.Time
		if _, err := createVaccinationReminderPlan(exec, models.ReminderSourceVaccinationExtra, userID, petID, vaccinationID, date, &timeOfDay, loc, tz, name, &nextMoment, now); err != nil {
			return err
		}
	}
	return nil
}

// validateCreateVaccinationExtraReminders проверяет дополнительные
// напоминания при создании: допустимы только при next_date; моменты строго в
// будущем, не позже следующей вакцинации и различны (с напоминанием на
// следующую дату — если оно создаётся).
func validateCreateVaccinationExtraReminders(req models.CreateVaccinationRequest, loc *time.Location, now time.Time) error {
	if len(req.ExtraReminders) == 0 {
		return nil
	}
	nextDate, _ := parseDateOnly(*req.NextDate)
	nextMoment := localMoment(nextDate, database.VaccinationTimeOfDay(req.NextTime), loc)
	hasMain := req.AddReminderOnNext != nil && *req.AddReminderOnNext
	extras := make([]vaccinationExtraMoment, 0, len(req.ExtraReminders))
	for _, item := range req.ExtraReminders {
		date, _ := parseDateOnly(item.Date)
		extras = append(extras, vaccinationExtraMoment{moment: localMoment(date, normalizeTimeOfDay(item.Time), loc), fresh: true})
	}
	return validateVaccinationExtraMoments(extras, nextMoment, true, hasMain, now)
}

// syncVaccinationExtraReminders приводит дополнительные напоминания в
// соответствие с PATCH (см. «Вакцинации — Backend», «Редактирование»):
//
//   - итоговой next_date нет — все существующие жёстко удаляются, непустой
//     extra_reminders — 400;
//   - extra_reminders передан — это полный итоговый набор: элемент с plan_id
//     обновляется на месте (переносится, если изменился момент), без plan_id —
//     создаётся, отсутствующие жёстко удаляются;
//   - не передан — существующие не меняются, но при изменении момента
//     следующей вакцинации проверяется, что ни одно не оказалось позже него.
//
// existing — текущие настройки дополнительных напоминаний (заблокированные).
// rename — изменилось имя: value.name обновляется у всех остающихся.
// Остающимся напоминаниям пересчитывается и value.vaccination_at, когда
// изменился момент следующей вакцинации (nextChanged). Возвращает ключи объектов S3 без ссылок.
func syncVaccinationExtraReminders(exec database.Executor, userID string, vaccination *models.VaccinationDB, req models.UpdateVaccinationRequest, existing []models.ReminderPlanDB, effectiveNextDate sql.NullTime, effectiveNextTime string, nextChanged, hasMain, rename bool, name string, loc *time.Location, tz string, now time.Time) ([]string, error) {
	deleteIDs := func(plans []models.ReminderPlanDB) ([]string, error) {
		ids := make([]uuid.UUID, 0, len(plans))
		for _, p := range plans {
			ids = append(ids, p.ID)
		}
		return database.DeleteReminderPlansWith(exec, ids)
	}

	if !effectiveNextDate.Valid {
		if req.ExtraReminders != nil && len(*req.ExtraReminders) > 0 {
			return nil, vaccinationBadRequest("Дополнительные напоминания допускаются только при заданной next_date")
		}
		return deleteIDs(existing)
	}
	nextMoment := localMoment(effectiveNextDate.Time, effectiveNextTime, loc)

	existingByID := make(map[uuid.UUID]models.ReminderPlanDB, len(existing))
	for _, p := range existing {
		existingByID[p.ID] = p
	}

	type scheduledPlan struct {
		plan models.ReminderPlanDB
		date time.Time
		time string
	}
	var keep []models.ReminderPlanDB // остаются без переноса
	var moved []scheduledPlan        // переносятся на новый момент
	var created []models.VaccinationExtraReminderRequest
	var finals []vaccinationExtraMoment
	remaining := make(map[uuid.UUID]models.ReminderPlanDB, len(existing))
	for id, p := range existingByID {
		remaining[id] = p
	}

	if req.ExtraReminders == nil {
		for _, p := range existing {
			keep = append(keep, p)
			finals = append(finals, vaccinationExtraMoment{moment: localMoment(p.StartDate, extraPlanTimeOfDay(p), loc)})
		}
		remaining = map[uuid.UUID]models.ReminderPlanDB{}
	} else {
		for _, item := range *req.ExtraReminders {
			date, _ := parseDateOnly(item.Date)
			timeOfDay := normalizeTimeOfDay(item.Time)
			moment := localMoment(date, timeOfDay, loc)
			if item.PlanID == nil || *item.PlanID == "" {
				created = append(created, item)
				finals = append(finals, vaccinationExtraMoment{moment: moment, fresh: true})
				continue
			}
			planID, _ := uuid.Parse(*item.PlanID)
			plan, ok := remaining[planID]
			if !ok {
				return nil, vaccinationBadRequest("plan_id не относится к дополнительным напоминаниям этой вакцинации")
			}
			delete(remaining, planID)
			if extraPlanMatches(plan, date, timeOfDay) {
				keep = append(keep, plan)
				finals = append(finals, vaccinationExtraMoment{moment: moment})
				continue
			}
			moved = append(moved, scheduledPlan{plan: plan, date: date, time: timeOfDay})
			finals = append(finals, vaccinationExtraMoment{moment: moment, fresh: true})
		}
	}

	if err := validateVaccinationExtraMoments(finals, nextMoment, nextChanged, hasMain, now); err != nil {
		return nil, err
	}

	var orphanKeys []string
	if len(remaining) > 0 {
		gone := make([]models.ReminderPlanDB, 0, len(remaining))
		for _, p := range existing {
			if _, ok := remaining[p.ID]; ok {
				gone = append(gone, p)
			}
		}
		keys, err := deleteIDs(gone)
		if err != nil {
			return nil, err
		}
		orphanKeys = append(orphanKeys, keys...)
	}
	for _, m := range moved {
		timeOfDay := m.time
		keys, err := rescheduleVaccinationReminderPlan(exec, m.plan, m.date, &timeOfDay, loc, tz, now)
		if err != nil {
			return nil, err
		}
		orphanKeys = append(orphanKeys, keys...)
	}
	if rename || nextChanged {
		value, err := vaccinationEventValue(name, &nextMoment, loc)
		if err != nil {
			return nil, err
		}
		updated := make([]models.ReminderPlanDB, 0, len(keep)+len(moved))
		updated = append(updated, keep...)
		for _, m := range moved {
			updated = append(updated, m.plan)
		}
		for _, p := range updated {
			if err := database.UpdateReminderPlanWith(exec, p.ID, database.ReminderPlanUpdate{Value: &value}); err != nil {
				return nil, err
			}
		}
	}
	if err := createVaccinationExtraReminders(exec, userID, vaccination.PetID, vaccination.ID, created, loc, tz, name, nextMoment, now); err != nil {
		return nil, err
	}
	return orphanKeys, nil
}

// groupVaccinationExtraReminders строит ответы дополнительных напоминаний по
// прививкам: по возрастанию момента, момент — в часовом поясе настроек (дата и
// время суток настроек разового расписания).
func groupVaccinationExtraReminders(plans []models.ReminderPlanDB) map[uuid.UUID][]models.VaccinationExtraReminderResponse {
	result := make(map[uuid.UUID][]models.VaccinationExtraReminderResponse)
	for _, p := range plans {
		if !p.SourceID.Valid {
			continue
		}
		result[p.SourceID.UUID] = append(result[p.SourceID.UUID], models.VaccinationExtraReminderResponse{
			PlanID: p.ID.String(),
			Date:   p.StartDate.Format("2006-01-02"),
			Time:   extraPlanTimeOfDay(p)[:5],
		})
	}
	for _, items := range result {
		sort.SliceStable(items, func(i, j int) bool {
			if items[i].Date != items[j].Date {
				return items[i].Date < items[j].Date
			}
			return items[i].Time < items[j].Time
		})
	}
	return result
}

// deleteVaccinationExtraRemindersWith жёстко удаляет все дополнительные
// напоминания прививки вместе с их напоминаниями и файлами. Возвращает ключи
// объектов S3 без ссылок.
func deleteVaccinationExtraRemindersWith(exec database.Executor, vaccinationID uuid.UUID) ([]string, error) {
	extras, err := database.ListVaccinationExtraPlansWith(exec, []uuid.UUID{vaccinationID}, true)
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0, len(extras))
	for _, p := range extras {
		ids = append(ids, p.ID)
	}
	return database.DeleteReminderPlansWith(exec, ids)
}

// applyScheduleToVaccination применяет новое разовое расписание настроек
// вакцинации (PATCH /reminder-plans/{id}, «Изменить все») к её записи и
// проверяет инвариант «момент дополнительного напоминания не позже момента
// следующей вакцинации»:
//
//   - напоминание на следующую вакцинацию (source = vaccination) —
//     дата записывается в next_date, время суток в next_time; перенос раньше
//     любого дополнительного напоминания либо на его момент — 400;
//   - дополнительное напоминание (source = vaccination_extra) — поля вакцинации не меняются; перенос позже
//     следующей вакцинации либо на момент другого напоминания вакцинации —
//     400.
//
// Моменты трактуются как местное время пояса loc запроса.
func applyScheduleToVaccination(tx *sql.Tx, plan models.ReminderPlanDB, spec reminderScheduleSpec, loc *time.Location) error {
	vaccination, err := database.GetVaccinationByIDWith(tx, plan.SourceID.UUID, true)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	timeOfDay := "00:00"
	if len(spec.Times) > 0 {
		timeOfDay = normalizeTimeOfDay(spec.Times[0].Time)
	}
	newMoment := localMoment(spec.StartDate, timeOfDay, loc)

	extras, err := database.ListVaccinationExtraPlansWith(tx, []uuid.UUID{vaccination.ID}, false)
	if err != nil {
		return err
	}

	isMain := plan.Source == models.ReminderSourceVaccination
	if isMain {
		for _, extra := range extras {
			moment := localMoment(extra.StartDate, extraPlanTimeOfDay(extra), loc)
			if !moment.Before(newMoment) {
				return vaccinationBadRequest("Момент напоминания на следующую вакцинацию не может быть раньше дополнительных напоминаний или совпадать с ними")
			}
		}
		if err := database.SetVaccinationNextMomentWith(tx, vaccination.ID, spec.StartDate, timeOfDay); err != nil {
			return err
		}
		return refreshVaccinationExtraMoments(tx, vaccination, extras, spec.StartDate, timeOfDay, loc)
	}

	if vaccination.NextDate.Valid {
		nextTime := "00:00"
		if vaccination.NextTime.Valid {
			nextTime = vaccination.NextTime.String
		}
		nextMoment := localMoment(vaccination.NextDate.Time, nextTime, loc)
		if newMoment.After(nextMoment) {
			return vaccinationBadRequest("Момент дополнительного напоминания не может быть позже даты и времени следующей вакцинации")
		}
		if vaccination.NextPlanID.Valid && newMoment.Equal(nextMoment) {
			return vaccinationBadRequest("Моменты напоминаний вакцинации не должны совпадать")
		}
	}
	for _, extra := range extras {
		if extra.ID == plan.ID {
			continue
		}
		if localMoment(extra.StartDate, extraPlanTimeOfDay(extra), loc).Equal(newMoment) {
			return vaccinationBadRequest("Моменты напоминаний вакцинации не должны совпадать")
		}
	}
	return nil
}

// vaccinationCompletion — завершение вакцинации отметкой «выполнено»
// напоминания на следующую вакцинацию.
type vaccinationCompletion struct {
	vaccinationID uuid.UUID
	extraPlanIDs  []uuid.UUID
}

// findVaccinationCompletion возвращает завершение вакцинации, если plan —
// напоминание на следующую вакцинацию (source = vaccination); для остальных
// настроек (в том числе дополнительного напоминания вакцинации, source =
// vaccination_extra, поля которой при отметке не меняются) возвращает nil. Вызывается до
// закрытия напоминания: вместе с опустевшими настройками ссылка next_plan_id
// обнуляется.
func findVaccinationCompletion(tx *sql.Tx, plan models.ReminderPlanDB) (*vaccinationCompletion, error) {
	if plan.Source != models.ReminderSourceVaccination || !plan.SourceID.Valid {
		return nil, nil
	}
	vaccination, err := database.GetVaccinationByIDWith(tx, plan.SourceID.UUID, true)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	extras, err := database.ListVaccinationExtraPlansWith(tx, []uuid.UUID{vaccination.ID}, true)
	if err != nil {
		return nil, err
	}
	completion := &vaccinationCompletion{vaccinationID: vaccination.ID}
	for _, extra := range extras {
		completion.extraPlanIDs = append(completion.extraPlanIDs, extra.ID)
	}
	return completion, nil
}

// apply очищает next_date и next_time вакцинации, записывает completed_at и
// жёстко удаляет её дополнительные напоминания. Возвращает ключи объектов S3
// без ссылок.
func (c *vaccinationCompletion) apply(tx *sql.Tx, now time.Time) ([]string, error) {
	if err := database.CompleteVaccinationWith(tx, c.vaccinationID, now); err != nil {
		return nil, err
	}
	return database.DeleteReminderPlansWith(tx, c.extraPlanIDs)
}

// refreshVaccinationExtraMoments пересчитывает value.vaccination_at у
// дополнительных напоминаний по итоговому моменту следующей вакцинации, если
// он изменился относительно сохранённого в вакцинации (перенос напоминания на
// следующую вакцинацию из календаря). Расписание самих дополнительных
// напоминаний не меняется.
func refreshVaccinationExtraMoments(exec database.Executor, vaccination *models.VaccinationDB, extras []models.ReminderPlanDB, nextDate time.Time, nextTime string, loc *time.Location) error {
	if len(extras) == 0 {
		return nil
	}
	storedTime := "00:00"
	if vaccination.NextTime.Valid {
		storedTime = normalizeTimeOfDay(vaccination.NextTime.String)
	}
	if vaccination.NextDate.Valid &&
		vaccination.NextDate.Time.Format("2006-01-02") == nextDate.Format("2006-01-02") &&
		storedTime == normalizeTimeOfDay(nextTime) {
		return nil
	}
	moment := localMoment(nextDate, nextTime, loc)
	value, err := vaccinationEventValue(vaccination.Name, &moment, loc)
	if err != nil {
		return err
	}
	for _, extra := range extras {
		if err := database.UpdateReminderPlanWith(exec, extra.ID, database.ReminderPlanUpdate{Value: &value}); err != nil {
			return err
		}
	}
	return nil
}

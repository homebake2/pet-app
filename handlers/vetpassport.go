// Package handlers: «Ведпаспорт» (медкарта питомца) — 5 pet_id-scoped
// сущностей (Vaccination, Disease, VetVisit, Allergy, Medication). Паттерн
// повторяет handlers/events.go/pet.go 1-в-1: soft-delete, ownership через
// resolveOwnedPet/CheckPetBelongsToUser, до 10 файлов через generic-механизм
// (handlers/files.go). PATCH/DELETE отвечают 204 без тела (как /events/{id});
// POST создания отвечает только {id} (GetXIdResponseRequest в spec.json) —
// полное представление сущности отдаётся только списком (GET /pet/{id}/...).
package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"myauthservice/database"
	"myauthservice/models"
	"myauthservice/openapi"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// owner_type для generic-механизма файлов сущностей (см. handlers/files.go).
// ---------------------------------------------------------------------------

const (
	vaccinationFileOwnerType = "vaccination_file"
	diseaseFileOwnerType     = "disease_file"
	vetVisitFileOwnerType    = "vet_visit_file"
	allergyFileOwnerType     = "allergy_file"
	medicationFileOwnerType  = "medication_file"

	// vetPassportFileMaxCount — лимит кардинальности «до N» для всех 5
	// owner_type ветпаспорта, тот же, что и у event_file.
	vetPassportFileMaxCount = 10
)

var vetPassportFileContentTypes = map[string]bool{
	"image/jpeg":      true,
	"image/png":       true,
	"image/webp":      true,
	"application/pdf": true,
}

// ---------------------------------------------------------------------------
// Диспетчер /pet/{id}/<child> (см. PetByIDHandler в handlers/pet.go).
// ---------------------------------------------------------------------------

type petChildResourceHandler struct {
	list   func(w http.ResponseWriter, r *http.Request, petID uuid.UUID)
	create func(w http.ResponseWriter, r *http.Request, petID uuid.UUID)
}

var petChildResourceHandlers = map[string]petChildResourceHandler{
	"vaccinations": {list: GetPetVaccinationsHandler, create: CreateVaccinationHandler},
	"diseases":     {list: GetPetDiseasesHandler, create: CreateDiseaseHandler},
	"vet-visits":   {list: GetPetVetVisitsHandler, create: CreateVetVisitHandler},
	"allergies":    {list: GetPetAllergiesHandler, create: CreateAllergyHandler},
	"medications":  {list: GetPetMedicationsHandler, create: CreateMedicationHandler},
}

// ---------------------------------------------------------------------------
// Общие хелперы.
// ---------------------------------------------------------------------------

// parseIDFromPath разбирает первый сегмент пути после prefix как UUID —
// обобщение parseEventIDFromPath/parsePetIDFromPath (handlers/ownership.go)
// для новых сущностей ветпаспорта.
func parseIDFromPath(r *http.Request, prefix string) (uuid.UUID, error) {
	segments := pathSegments(r, prefix)
	if len(segments) == 0 {
		return uuid.Nil, fmt.Errorf("пустой id")
	}
	return uuid.Parse(segments[0])
}

func parseDateOnly(s string) (time.Time, error) {
	return time.Parse("2006-01-02", s)
}

func isValidDateOnly(s string) bool {
	_, err := parseDateOnly(s)
	return err == nil
}

var timeOfDayRe = regexp.MustCompile(`^([01]\d|2[0-3]):([0-5]\d)(:([0-5]\d))?$`)

// isValidTimeOfDay проверяет формат "HH:MM" или "HH:MM:SS" (OpenAPI
// format=time), используемый administered_time/next_time в
// GetVaccinationRequest.
func isValidTimeOfDay(s string) bool {
	return timeOfDayRe.MatchString(s)
}

// combineDateAndTime строит момент времени из календарной даты и
// опционального времени суток (по умолчанию — полночь), трактуя их как
// местное время пояса loc: клиент присылает дату и "HH:MM" так, как их видит
// пользователь, а пояс — параметром tz (см. parseTimeZoneParam).
// Используется при создании/переносе записей, связанных с прививкой.
// Несуществующее местное время и повторяющийся час при переходе на
// летнее/зимнее время разрешаются правилами localMoment.
func combineDateAndTime(date time.Time, timeOfDay *string, loc *time.Location) time.Time {
	if timeOfDay == nil || *timeOfDay == "" {
		return localMoment(date, "00:00", loc)
	}
	return localMoment(date, *timeOfDay, loc)
}

// eventDateString — момент времени в формате поля date запроса создания
// события (RFC3339, UTC).
func eventDateString(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// truncateRunes обрезает строку до maxLen рун (не байт) — используется для
// value.label синтетических событий "other", создаваемых по прививке: имя
// прививки может быть длиннее лимита label (см. eventreg "other": 1-50).
func truncateRunes(s string, maxLen int) string {
	r := []rune(s)
	if len(r) <= maxLen {
		return s
	}
	return string(r[:maxLen])
}

// vaccinationEventLabelPrefix — префикс подписи события-напоминания по
// умолчанию: value.label = "Вакцинация: " + name (см. «Вакцинации —
// Backend», шаг 4 создания).
const vaccinationEventLabelPrefix = "Вакцинация: "

// otherEventLabelMaxRunes — лимит value.label события type=other (см.
// eventreg "other": 1-50).
const otherEventLabelMaxRunes = 50

// vaccinationEventLabel возвращает подпись связанного события прививки.
// У сервера нет локали пользователя, поэтому клиент может передать уже
// локализованную подпись в event_label (например, "Vaccination: Rabies") —
// так сетевой режим даёт ту же подпись, что и локальный (dataSource=local),
// где её формирует i18n клиента. Если event_label не передан или пуст —
// подпись по умолчанию "Вакцинация: " + name.
func vaccinationEventLabel(requested *string, name string) string {
	if requested != nil && strings.TrimSpace(*requested) != "" {
		return *requested
	}
	return vaccinationEventLabelPrefix + name
}

// otherEventValue строит value события type=other: {"label": <label,
// обрезано до 50 рун>}.
func otherEventValue(label string) (json.RawMessage, error) {
	return json.Marshal(map[string]string{"label": truncateRunes(label, otherEventLabelMaxRunes)})
}

// vaccinationFactMoment строит момент факта на дату введения и проверяет
// правило факта: он не может быть позднее текущего момента (с допуском на
// расхождение часов). Возвращает reminderHTTPError с кодом 400.
func vaccinationFactMoment(date time.Time, timeOfDay *string, loc *time.Location) (time.Time, error) {
	moment := combineDateAndTime(date, timeOfDay, loc)
	if msg := validateFactDate(moment); msg != "" {
		return time.Time{}, newReminderHTTPError(http.StatusBadRequest, openapi.VALIDATIONERROR, "Дата введения вакцинации не может быть в будущем")
	}
	return moment, nil
}

// createVaccinationFact создаёт факт type=other со значением
// {"label": <label, обрезано до 50 рун>} на дату введения прививки —
// побочный эффект POST/PATCH /pet/{id}/vaccinations (факт создаётся
// всегда). Ошибка возвращается вызывающему: создание
// факта входит в основной контракт ответа (administered_event_id).
func createVaccinationFact(exec database.Executor, userID string, petID uuid.UUID, date time.Time, timeOfDay *string, loc *time.Location, label string) (uuid.UUID, error) {
	value, err := otherEventValue(label)
	if err != nil {
		return uuid.Nil, err
	}
	moment, err := vaccinationFactMoment(date, timeOfDay, loc)
	if err != nil {
		return uuid.Nil, err
	}
	return database.InsertEventWith(exec, userID, []uuid.UUID{petID}, models.EventFields{
		Date:  eventDateString(moment),
		Type:  "other",
		Value: value,
	}, "")
}

// vaccinationReminderMoment строит момент напоминания на дату следующей
// вакцинации и проверяет, что он строго в будущем; возвращает
// нормализованное время суток для хранения в настройках.
func vaccinationReminderMoment(nextDate time.Time, timeOfDay *string, loc *time.Location, now time.Time) (moment time.Time, normalizedTime string, err error) {
	normalizedTime = "00:00"
	if timeOfDay != nil && *timeOfDay != "" {
		normalizedTime = normalizeTimeOfDay(*timeOfDay)
	}
	moment = localMoment(nextDate, normalizedTime, loc)
	if !moment.After(now) {
		return time.Time{}, "", newReminderHTTPError(http.StatusBadRequest, openapi.VALIDATIONERROR, "Момент напоминания на дату следующей вакцинации должен быть в будущем")
	}
	return moment, normalizedTime, nil
}

// createVaccinationReminderPlan создаёт настройки напоминания на дату
// следующей вакцинации: source=vaccination, разовое расписание на
// next_date, одно напоминание. Возвращает id настроек.
func createVaccinationReminderPlan(exec database.Executor, userID string, petID, vaccinationID uuid.UUID, nextDate time.Time, timeOfDay *string, loc *time.Location, tz, label string, now time.Time) (uuid.UUID, error) {
	value, err := otherEventValue(label)
	if err != nil {
		return uuid.Nil, err
	}
	moment, normalizedTime, err := vaccinationReminderMoment(nextDate, timeOfDay, loc, now)
	if err != nil {
		return uuid.Nil, err
	}
	ownerID, err := uuid.Parse(userID)
	if err != nil {
		return uuid.Nil, err
	}
	planID := uuid.New()
	plan := models.ReminderPlanDB{
		ID:            planID,
		UserID:        ownerID,
		Source:        models.ReminderSourceVaccination,
		SourceID:      uuid.NullUUID{UUID: vaccinationID, Valid: true},
		Type:          "other",
		Value:         value,
		FrequencyType: models.ReminderFrequencyOnce,
		Times:         []string{normalizedTime},
		StartDate:     nextDate,
		TZ:            tz,
	}
	if err := database.InsertReminderPlanWith(exec, plan, []uuid.UUID{petID}, []models.ReminderMoment{{RemindAt: moment.UTC()}}); err != nil {
		return uuid.Nil, err
	}
	return planID, nil
}

// rescheduleVaccinationReminderPlan переносит существующие настройки
// напоминания вакцинации на новый момент (разовое расписание — как
// изменение расписания в «Напоминания — Backend», эндпоинт C): будущее
// незавершённое напоминание заменяется, закрытые и прошедшие не
// затрагиваются. Новый момент обязан быть строго в будущем. Возвращает ключи
// объектов S3 без ссылок.
func rescheduleVaccinationReminderPlan(exec database.Executor, plan models.ReminderPlanDB, nextDate time.Time, timeOfDay *string, loc *time.Location, tz string, now time.Time) ([]string, error) {
	_, normalizedTime, err := vaccinationReminderMoment(nextDate, timeOfDay, loc, now)
	if err != nil {
		return nil, err
	}
	spec := reminderScheduleSpec{
		FrequencyType: models.ReminderFrequencyOnce,
		Times:         []reminderTimeSlot{{Time: normalizedTime}},
		StartDate:     nextDate,
	}
	closed, err := database.ListClosedRemindMomentsWith(exec, plan.ID)
	if err != nil {
		return nil, err
	}
	moments := computeReminderMoments(spec, loc, now, closed, models.ReminderMaxMomentsPerOperation)
	if len(moments) == 0 {
		return nil, newReminderHTTPError(http.StatusBadRequest, openapi.VALIDATIONERROR, "Момент напоминания на дату следующей вакцинации должен быть в будущем")
	}
	orphanKeys, err := database.DeleteFutureOpenRemindersWith(exec, plan.ID, now)
	if err != nil {
		return nil, err
	}
	if _, err := database.InsertRemindersWith(exec, plan.ID, moments); err != nil {
		return nil, err
	}
	err = database.UpdateReminderPlanWith(exec, plan.ID, database.ReminderPlanUpdate{Schedule: planScheduleFromSpec(spec, tz)})
	return orphanKeys, err
}

// syncVaccinationFact приводит факт прививки на дату введения в соответствие
// с PATCH — см. «Вакцинации — Backend», раздел «Редактирование»: связь «одна
// прививка — один факт», поэтому существующий факт обновляется на месте, а не
// пересоздаётся. Факт есть всегда, пока есть дата введения:
//
//   - связанного факта нет (или он удалён, например, из календаря) и в
//     запросе переданы administered_date либо administered_time — создаётся
//     новый;
//   - факт существует и в запросе изменилась дата (dateChanged), передано
//     время либо изменилась подпись (relabel) — обновляются его дата/время и
//     подпись; время суток, не переданное в этом запросе, сохраняется прежним
//     (то, которое факт имеет в поясе loc);
//   - иначе факт не трогается.
//
// Итоговый момент факта не может быть позднее текущего (правило факта).
// Дата и время суток трактуются как местное время пояса loc.
//
// Возвращает новое значение ссылки на факт для UpdateVaccinationWith, либо
// nil, если ссылку менять не нужно.
func syncVaccinationFact(exec database.Executor, userID string, petID uuid.UUID, currentEventID uuid.NullUUID, date sql.NullTime, dateChanged bool, administeredTime *string, relabel bool, loc *time.Location, label string) (*uuid.NullUUID, error) {
	var existing *models.EventDB
	if currentEventID.Valid {
		event, err := database.GetEventByIDForUpdateWith(exec, currentEventID.UUID)
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		if err == nil {
			existing = event
		}
	}

	hasTime := administeredTime != nil && *administeredTime != ""

	if existing == nil {
		if !date.Valid || (!dateChanged && !hasTime) {
			return nil, nil
		}
		newEventID, err := createVaccinationFact(exec, userID, petID, date.Time, administeredTime, loc, label)
		if err != nil {
			return nil, err
		}
		return &uuid.NullUUID{UUID: newEventID, Valid: true}, nil
	}

	if !dateChanged && !hasTime && !relabel {
		return nil, nil
	}

	timeOfDay := existing.Date.In(loc).Format("15:04:05")
	if hasTime {
		timeOfDay = *administeredTime
	}
	dateTime, err := vaccinationFactMoment(date.Time, &timeOfDay, loc)
	if err != nil {
		return nil, err
	}
	dateTime = dateTime.UTC()
	var value *json.RawMessage
	if relabel {
		v, err := otherEventValue(label)
		if err != nil {
			return nil, err
		}
		value = &v
	}
	if err := database.UpdateEventWith(exec, existing.ID, &dateTime, nil, nil, value); err != nil {
		return nil, err
	}
	return nil, nil
}

// syncVaccinationReminderPlan приводит настройки напоминания на дату
// следующей вакцинации в соответствие с PATCH — см. «Вакцинации — Backend»,
// раздел «Редактирование»:
//
//   - next_date очищена — настройки жёстко удаляются независимо от флага;
//   - add_reminder_on_next=false — настройки жёстко удаляются;
//   - add_reminder_on_next=true без next_date — настройки удаляются (не
//     ошибка);
//   - add_reminder_on_next=true, настроек нет — создаются новые (момент
//     обязан быть в будущем);
//   - настройки есть и переданы next_date либо next_time — расписание
//     переносится на новый момент (подпись при этом меняется только вместе с
//     флагом true).
//
// Возвращает новое значение ссылки на настройки для UpdateVaccinationWith,
// либо nil, если ссылку менять не нужно, и ключи объектов S3 без ссылок.
func syncVaccinationReminderPlan(exec database.Executor, userID string, vaccination *models.VaccinationDB, req models.UpdateVaccinationRequest, effectiveNextDate sql.NullTime, loc *time.Location, tz, label string, now time.Time) (link *uuid.NullUUID, orphanKeys []string, err error) {
	var existing *database.ReminderPlanFull
	if vaccination.NextPlanID.Valid {
		plan, lookupErr := database.GetReminderPlanForUserWith(exec, vaccination.NextPlanID.UUID, userID, true)
		if lookupErr != nil && lookupErr != sql.ErrNoRows {
			return nil, nil, lookupErr
		}
		if lookupErr == nil {
			existing = plan
		}
	}

	deleteExisting := func() (*uuid.NullUUID, []string, error) {
		if existing == nil {
			return nil, nil, nil
		}
		keys, err := database.DeleteReminderPlanWith(exec, existing.ID)
		if err != nil {
			return nil, nil, err
		}
		return &uuid.NullUUID{}, keys, nil
	}

	nextDateCleared := req.NextDate != nil && *req.NextDate == ""
	flag := req.AddReminderOnNext
	if nextDateCleared || (flag != nil && !*flag) || (flag != nil && *flag && !effectiveNextDate.Valid) {
		return deleteExisting()
	}

	hasNextTime := req.NextTime != nil && *req.NextTime != ""
	nextDateGiven := req.NextDate != nil && *req.NextDate != ""

	if existing == nil {
		if flag == nil || !*flag {
			return nil, nil, nil
		}
		planID, err := createVaccinationReminderPlan(exec, userID, vaccination.PetID, vaccination.ID, effectiveNextDate.Time, req.NextTime, loc, tz, label, now)
		if err != nil {
			return nil, nil, err
		}
		return &uuid.NullUUID{UUID: planID, Valid: true}, nil, nil
	}

	if flag != nil && *flag {
		value, err := otherEventValue(label)
		if err != nil {
			return nil, nil, err
		}
		if err := database.UpdateReminderPlanWith(exec, existing.ID, database.ReminderPlanUpdate{Value: &value}); err != nil {
			return nil, nil, err
		}
	}

	if (nextDateGiven || hasNextTime) && effectiveNextDate.Valid {
		timeOfDay := req.NextTime
		if !hasNextTime && len(existing.Times) > 0 {
			t := existing.Times[0]
			timeOfDay = &t
		}
		keys, err := rescheduleVaccinationReminderPlan(exec, existing.ReminderPlanDB, effectiveNextDate.Time, timeOfDay, loc, tz, now)
		if err != nil {
			return nil, nil, err
		}
		return nil, keys, nil
	}
	return nil, nil, nil
}

// writeVetPassportTxError превращает ошибку транзакции в ответ: ошибки с
// HTTP-статусом — как есть, sql.ErrNoRows — 404, остальное — 500.
func writeVetPassportTxError(w http.ResponseWriter, err error, notFoundMessage, internalMessage string) {
	var httpErr *reminderHTTPError
	if errors.As(err, &httpErr) {
		writeError(w, httpErr.status, httpErr.code, httpErr.message)
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, openapi.NOTFOUND, notFoundMessage)
		return
	}
	writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, internalMessage)
}

// resolvePetForVetPassportCreate проверяет, что питомец petID существует,
// принадлежит userID и не удалён — то же правило, что и у CreateEventHandler
// (handlers/events.go): создание дочерней сущности медкарты для удалённого
// питомца запрещено отдельной ошибкой 400, а не общим 404.
func resolvePetForVetPassportCreate(w http.ResponseWriter, petID uuid.UUID, userID string) (ok bool) {
	petDB, err := database.GetPetIdDBByIDAndUserID(petID, userID)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, openapi.NOTFOUND, fmt.Sprintf("Питомец %s не найден", petID))
			return false
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения питомца")
		return false
	}
	if petDB.DeletedAt.Valid {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Невозможно создать запись, так как питомец удален")
		return false
	}
	return true
}

// replayVetPassportCreate реализует дедупликацию POST создания сущности
// ветпаспорта по заголовку Idempotency-Key — тот же механизм, что у
// POST /events: ключ хранится в строке сущности, уникален на пару
// (pet_id, idempotency_key). Если запись с таким ключом уже есть, отвечает
// тем же `201 {id}`, что и первый успешный запрос, и возвращает true —
// вызывающий хендлер на этом завершается. Вызывается дважды: перед вставкой
// (обычный повтор после обрыва соединения) и после нарушения уникального
// индекса при вставке (гонка параллельных запросов с одним ключом).
// Возвращает false, если ключ не передан или запись не найдена (тогда
// создание продолжается как обычно); ошибка поиска — 500 и true.
func replayVetPassportCreate(w http.ResponseWriter, table database.VetPassportTable, petID uuid.UUID, idempotencyKey string) (handled bool) {
	if idempotencyKey == "" {
		return false
	}
	existingID, err := database.GetVetPassportEntityIDByIdempotencyKey(table, petID, idempotencyKey)
	if err == sql.ErrNoRows {
		return false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка проверки idempotency key")
		return true
	}
	writeJSON(w, http.StatusCreated, models.IDResponse{ID: existingID.String()})
	return true
}

// ---------------------------------------------------------------------------
// Vaccination
// ---------------------------------------------------------------------------

func vaccinationResponseFromDB(v models.VaccinationDB, filesCount int) models.VaccinationResponse {
	resp := models.VaccinationResponse{
		ID:               v.ID.String(),
		PetID:            v.PetID.String(),
		Name:             v.Name,
		AdministeredDate: v.AdministeredDate.Format("2006-01-02"),
		FilesCount:       filesCount,
	}
	if v.NextDate.Valid {
		s := v.NextDate.Time.Format("2006-01-02")
		resp.NextDate = &s
	}
	if v.AdministeredEventID.Valid {
		s := v.AdministeredEventID.UUID.String()
		resp.AdministeredEventID = &s
	}
	if v.NextPlanID.Valid {
		s := v.NextPlanID.UUID.String()
		resp.NextPlanID = &s
	}
	return resp
}

func validateCreateVaccinationRequest(req models.CreateVaccinationRequest) string {
	if strings.TrimSpace(req.Name) == "" {
		return "Поле name обязательно"
	}
	if len(req.Name) > models.VaccinationNameMaxLen {
		return "Поле name превышает допустимую длину"
	}
	if req.AdministeredDate == "" || !isValidDateOnly(req.AdministeredDate) {
		return "Некорректный формат administered_date, ожидается YYYY-MM-DD"
	}
	if req.NextDate != nil && *req.NextDate != "" && !isValidDateOnly(*req.NextDate) {
		return "Некорректный формат next_date, ожидается YYYY-MM-DD"
	}
	if req.AdministeredTime != nil && *req.AdministeredTime != "" && !isValidTimeOfDay(*req.AdministeredTime) {
		return "Некорректный формат administered_time, ожидается HH:MM[:SS]"
	}
	if req.NextTime != nil && *req.NextTime != "" && !isValidTimeOfDay(*req.NextTime) {
		return "Некорректный формат next_time, ожидается HH:MM[:SS]"
	}
	if req.EventLabel != nil && utf8.RuneCountInString(*req.EventLabel) > models.VaccinationEventLabelMaxLen {
		return "Поле event_label превышает допустимую длину"
	}
	return ""
}

// GetPetVaccinationsHandler обрабатывает GET /pet/{id}/vaccinations.
func GetPetVaccinationsHandler(w http.ResponseWriter, r *http.Request, petID uuid.UUID) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	limit, offset, ok := parsePetEventsPaging(r)
	if !ok {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Параметры limit/offset должны быть неотрицательными целыми числами")
		return
	}
	if _, ok := resolveOwnedPet(w, petID, userID); !ok {
		return
	}

	items, err := database.GetVaccinationsByPetID(petID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения прививок")
		return
	}

	ids := make([]uuid.UUID, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	filesCounts, err := database.CountFilesForOwners(vaccinationFileOwnerType, ids)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения количества файлов")
		return
	}

	resp := models.VaccinationListResponse{Items: make([]models.VaccinationResponse, 0, len(items))}
	for _, it := range items {
		resp.Items = append(resp.Items, vaccinationResponseFromDB(it, filesCounts[it.ID]))
	}
	writeJSON(w, http.StatusOK, resp)
}

// CreateVaccinationHandler обрабатывает POST /pet/{id}/vaccinations.
func CreateVaccinationHandler(w http.ResponseWriter, r *http.Request, petID uuid.UUID) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var req models.CreateVaccinationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректное тело запроса")
		return
	}

	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey != "" && !isValidUUIDv4(idempotencyKey) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Заголовок Idempotency-Key должен быть валидным UUID v4")
		return
	}

	if msg := validateCreateVaccinationRequest(req); msg != "" {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, msg)
		return
	}
	loc, ok := parseTimeZoneParam(w, r)
	if !ok {
		return
	}
	tz := r.URL.Query().Get("tz")

	if !resolvePetForVetPassportCreate(w, petID, userID) {
		return
	}
	if replayVetPassportCreate(w, database.VaccinationTable, petID, idempotencyKey) {
		return
	}

	administeredDate, _ := parseDateOnly(req.AdministeredDate)
	eventLabel := vaccinationEventLabel(req.EventLabel, req.Name)
	now := time.Now().UTC()

	// Факт на дату введения (создаётся всегда), прививка и настройки
	// напоминания создаются в одной транзакции: проигравший гонку параллельных запросов с одним
	// Idempotency-Key не оставляет после себя ни фактов, ни напоминаний.
	var newID uuid.UUID
	var administeredEventID, nextPlanID uuid.NullUUID
	err := database.RunInTx(func(tx *sql.Tx) error {
		factID, err := createVaccinationFact(tx, userID, petID, administeredDate, req.AdministeredTime, loc, eventLabel)
		if err != nil {
			return err
		}
		administeredEventID = uuid.NullUUID{UUID: factID, Valid: true}

		id, err := database.InsertVaccinationWith(tx, petID, req, administeredEventID, idempotencyKey)
		if err != nil {
			return err
		}
		newID = id

		if req.AddReminderOnNext != nil && *req.AddReminderOnNext && req.NextDate != nil && *req.NextDate != "" {
			nextDate, _ := parseDateOnly(*req.NextDate)
			planID, err := createVaccinationReminderPlan(tx, userID, petID, newID, nextDate, req.NextTime, loc, tz, eventLabel, now)
			if err != nil {
				return err
			}
			nextPlanID = uuid.NullUUID{UUID: planID, Valid: true}
			return database.SetVaccinationLinksWith(tx, newID, administeredEventID, nextPlanID)
		}
		return nil
	})
	if err != nil {
		if idempotencyKey != "" && database.IsUniqueViolation(err) && replayVetPassportCreate(w, database.VaccinationTable, petID, idempotencyKey) {
			return
		}
		writeVetPassportTxError(w, err, "Прививка не найдена", "Не удалось создать прививку")
		return
	}

	writeJSON(w, http.StatusCreated, models.VaccinationCreatedResponse{
		ID:                 newID.String(),
		VaccinationLinkIDs: vaccinationLinkIDs(administeredEventID, nextPlanID),
	})
}

// vaccinationLinkIDs преобразует ссылки на связанные записи в тело ответа
// (невалидная ссылка — null).
func vaccinationLinkIDs(administered, nextPlan uuid.NullUUID) models.VaccinationLinkIDs {
	toPtr := func(id uuid.NullUUID) *string {
		if !id.Valid {
			return nil
		}
		s := id.UUID.String()
		return &s
	}
	return models.VaccinationLinkIDs{AdministeredEventID: toPtr(administered), NextPlanID: toPtr(nextPlan)}
}

// VaccinationByIDHandler обрабатывает /vaccinations/{id}: PATCH/DELETE.
func VaccinationByIDHandler(w http.ResponseWriter, r *http.Request) {
	id, err := parseIDFromPath(r, "/vaccinations/")
	if err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректный id прививки")
		return
	}
	switch r.Method {
	case http.MethodPatch:
		UpdateVaccinationHandler(w, r, id)
	case http.MethodDelete:
		DeleteVaccinationHandler(w, r, id)
	default:
		writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
	}
}

func UpdateVaccinationHandler(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	vaccination, err := database.GetVaccinationByIDForUpdate(id)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Прививка не найдена")
			return
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения прививки")
		return
	}

	belongs, err := database.CheckPetBelongsToUser(vaccination.PetID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка проверки прав доступа")
		return
	}
	if !belongs {
		writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Прививка не найдена")
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректное тело запроса")
		return
	}
	var req models.UpdateVaccinationRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректное тело запроса")
		return
	}
	// next_date в контракте nullable: явный JSON null означает «очистить
	// дату» — так же, как пустая строка. encoding/json не отличает null от
	// отсутствующего поля для *string, поэтому проверяем сырой JSON.
	var rawFields map[string]json.RawMessage
	if err := json.Unmarshal(body, &rawFields); err == nil {
		if raw, present := rawFields["next_date"]; present && string(raw) == "null" {
			empty := ""
			req.NextDate = &empty
		}
	}

	if req.Name != nil && (strings.TrimSpace(*req.Name) == "" || len(*req.Name) > models.VaccinationNameMaxLen) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректное значение name")
		return
	}
	if req.AdministeredDate != nil && !isValidDateOnly(*req.AdministeredDate) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректный формат administered_date, ожидается YYYY-MM-DD")
		return
	}
	if req.NextDate != nil && *req.NextDate != "" && !isValidDateOnly(*req.NextDate) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректный формат next_date, ожидается YYYY-MM-DD")
		return
	}
	if req.AdministeredTime != nil && *req.AdministeredTime != "" && !isValidTimeOfDay(*req.AdministeredTime) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректный формат administered_time, ожидается HH:MM[:SS]")
		return
	}
	if req.NextTime != nil && *req.NextTime != "" && !isValidTimeOfDay(*req.NextTime) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректный формат next_time, ожидается HH:MM[:SS]")
		return
	}
	if req.EventLabel != nil && utf8.RuneCountInString(*req.EventLabel) > models.VaccinationEventLabelMaxLen {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поле event_label превышает допустимую длину")
		return
	}
	loc, ok := parseTimeZoneParam(w, r)
	if !ok {
		return
	}
	tz := r.URL.Query().Get("tz")

	effectiveName := vaccination.Name
	if req.Name != nil {
		effectiveName = *req.Name
	}
	eventLabel := vaccinationEventLabel(req.EventLabel, effectiveName)

	effectiveAdministeredDate := sql.NullTime{Time: vaccination.AdministeredDate, Valid: true}
	if req.AdministeredDate != nil {
		t, _ := parseDateOnly(*req.AdministeredDate)
		effectiveAdministeredDate = sql.NullTime{Time: t, Valid: true}
	}
	effectiveNextDate := vaccination.NextDate
	if req.NextDate != nil {
		effectiveNextDate = sql.NullTime{}
		if *req.NextDate != "" {
			t, _ := parseDateOnly(*req.NextDate)
			effectiveNextDate = sql.NullTime{Time: t, Valid: true}
		}
	}

	now := time.Now().UTC()
	currentAdministered, currentNext := vaccination.AdministeredEventID, vaccination.NextPlanID
	var orphanKeys []string
	err = database.RunInTx(func(tx *sql.Tx) error {
		locked, err := database.GetVaccinationByIDWith(tx, id, true)
		if err != nil {
			return err
		}

		administeredLink, err := syncVaccinationFact(tx, userID, locked.PetID, locked.AdministeredEventID, effectiveAdministeredDate, req.AdministeredDate != nil, req.AdministeredTime, req.Name != nil || req.EventLabel != nil, loc, eventLabel)
		if err != nil {
			return err
		}
		nextLink, keys, err := syncVaccinationReminderPlan(tx, userID, locked, req, effectiveNextDate, loc, tz, eventLabel, now)
		if err != nil {
			return err
		}
		orphanKeys = keys

		if err := database.UpdateVaccinationWith(tx, id, req, administeredLink, nextLink); err != nil {
			return err
		}

		// nil от sync-функций — ссылка не менялась, остаётся прежней.
		currentAdministered, currentNext = locked.AdministeredEventID, locked.NextPlanID
		if administeredLink != nil {
			currentAdministered = *administeredLink
		}
		if nextLink != nil {
			currentNext = *nextLink
		}
		return nil
	})
	if err != nil {
		writeVetPassportTxError(w, err, "Прививка не найдена", "Не удалось обновить прививку")
		return
	}
	deleteOrphanedObjects(r.Context(), orphanKeys)

	writeJSON(w, http.StatusOK, vaccinationLinkIDs(currentAdministered, currentNext))
}

func DeleteVaccinationHandler(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	vaccination, err := database.GetVaccinationByIDForUpdate(id)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Прививка не найдена")
			return
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения прививки")
		return
	}
	belongs, err := database.CheckPetBelongsToUser(vaccination.PetID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка проверки прав доступа")
		return
	}
	if !belongs {
		writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Прививка не найдена")
		return
	}

	// Удаление прививки мягко удаляет связанный факт на дату введения и
	// жёстко удаляет настройки напоминания на следующую дату (см.
	// «Вакцинации — Backend», раздел «Удаление») — в одной транзакции.
	var orphanKeys []string
	err = database.RunInTx(func(tx *sql.Tx) error {
		if err := database.SoftDeleteVaccinationWith(tx, id); err != nil {
			return err
		}
		if vaccination.AdministeredEventID.Valid {
			if err := database.DeleteEventWith(tx, vaccination.AdministeredEventID.UUID); err != nil && err != sql.ErrNoRows {
				return err
			}
		}
		if vaccination.NextPlanID.Valid {
			keys, err := database.DeleteReminderPlanWith(tx, vaccination.NextPlanID.UUID)
			if err != nil {
				return err
			}
			orphanKeys = keys
		}
		return nil
	})
	if err != nil {
		writeVetPassportTxError(w, err, "Прививка не найдена", "Ошибка удаления прививки")
		return
	}
	deleteOrphanedObjects(r.Context(), orphanKeys)
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Disease
// ---------------------------------------------------------------------------

func diseaseResponseFromDB(d models.DiseaseDB, filesCount int) models.DiseaseResponse {
	resp := models.DiseaseResponse{
		ID:            d.ID.String(),
		PetID:         d.PetID.String(),
		Name:          d.Name,
		DiagnosedDate: d.DiagnosedDate.Format("2006-01-02"),
		Status:        d.Status,
		FilesCount:    filesCount,
	}
	if d.Note.Valid {
		resp.Note = &d.Note.String
	}
	return resp
}

func validateCreateDiseaseRequest(req models.CreateDiseaseRequest) string {
	if strings.TrimSpace(req.Name) == "" || len(req.Name) > models.DiseaseNameMaxLen {
		return "Некорректное значение name"
	}
	if req.DiagnosedDate == "" || !isValidDateOnly(req.DiagnosedDate) {
		return "Некорректный формат diagnosed_date, ожидается YYYY-MM-DD"
	}
	if !models.IsValidDiseaseStatus(req.Status) {
		return "Некорректное значение status"
	}
	if req.Note != nil && len(*req.Note) > models.DiseaseNoteMaxLen {
		return "Поле note превышает допустимую длину"
	}
	return ""
}

func GetPetDiseasesHandler(w http.ResponseWriter, r *http.Request, petID uuid.UUID) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	limit, offset, ok := parsePetEventsPaging(r)
	if !ok {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Параметры limit/offset должны быть неотрицательными целыми числами")
		return
	}
	if _, ok := resolveOwnedPet(w, petID, userID); !ok {
		return
	}
	items, err := database.GetDiseasesByPetID(petID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения заболеваний")
		return
	}
	ids := make([]uuid.UUID, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	filesCounts, err := database.CountFilesForOwners(diseaseFileOwnerType, ids)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения количества файлов")
		return
	}
	resp := models.DiseaseListResponse{Items: make([]models.DiseaseResponse, 0, len(items))}
	for _, it := range items {
		resp.Items = append(resp.Items, diseaseResponseFromDB(it, filesCounts[it.ID]))
	}
	writeJSON(w, http.StatusOK, resp)
}

func CreateDiseaseHandler(w http.ResponseWriter, r *http.Request, petID uuid.UUID) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	var req models.CreateDiseaseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректное тело запроса")
		return
	}
	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey != "" && !isValidUUIDv4(idempotencyKey) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Заголовок Idempotency-Key должен быть валидным UUID v4")
		return
	}
	if msg := validateCreateDiseaseRequest(req); msg != "" {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, msg)
		return
	}
	if !resolvePetForVetPassportCreate(w, petID, userID) {
		return
	}
	if replayVetPassportCreate(w, database.DiseaseTable, petID, idempotencyKey) {
		return
	}
	newID, err := database.InsertDisease(petID, req, idempotencyKey)
	if err != nil {
		if idempotencyKey != "" && database.IsUniqueViolation(err) && replayVetPassportCreate(w, database.DiseaseTable, petID, idempotencyKey) {
			return
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось создать заболевание")
		return
	}
	writeJSON(w, http.StatusCreated, models.IDResponse{ID: newID.String()})
}

func DiseaseByIDHandler(w http.ResponseWriter, r *http.Request) {
	id, err := parseIDFromPath(r, "/diseases/")
	if err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректный id заболевания")
		return
	}
	switch r.Method {
	case http.MethodPatch:
		UpdateDiseaseHandler(w, r, id)
	case http.MethodDelete:
		DeleteDiseaseHandler(w, r, id)
	default:
		writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
	}
}

func UpdateDiseaseHandler(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	disease, err := database.GetDiseaseByIDForUpdate(id)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Заболевание не найдено")
			return
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения заболевания")
		return
	}
	belongs, err := database.CheckPetBelongsToUser(disease.PetID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка проверки прав доступа")
		return
	}
	if !belongs {
		writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Заболевание не найдено")
		return
	}
	var req models.UpdateDiseaseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректное тело запроса")
		return
	}
	if req.Name != nil && (strings.TrimSpace(*req.Name) == "" || len(*req.Name) > models.DiseaseNameMaxLen) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректное значение name")
		return
	}
	if req.DiagnosedDate != nil && !isValidDateOnly(*req.DiagnosedDate) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректный формат diagnosed_date, ожидается YYYY-MM-DD")
		return
	}
	if req.Status != nil && !models.IsValidDiseaseStatus(*req.Status) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректное значение status")
		return
	}
	if req.Note != nil && len(*req.Note) > models.DiseaseNoteMaxLen {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поле note превышает допустимую длину")
		return
	}
	if err := database.UpdateDisease(id, req); err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка обновления заболевания")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func DeleteDiseaseHandler(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	disease, err := database.GetDiseaseByIDForUpdate(id)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Заболевание не найдено")
			return
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения заболевания")
		return
	}
	belongs, err := database.CheckPetBelongsToUser(disease.PetID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка проверки прав доступа")
		return
	}
	if !belongs {
		writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Заболевание не найдено")
		return
	}
	if err := database.SoftDeleteDisease(id); err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Заболевание не найдено")
			return
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка удаления заболевания")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// VetVisit
// ---------------------------------------------------------------------------

func vetVisitResponseFromDB(v models.VetVisitDB, filesCount int) models.VetVisitResponse {
	resp := models.VetVisitResponse{
		ID:         v.ID.String(),
		PetID:      v.PetID.String(),
		VisitDate:  v.VisitDate.Format("2006-01-02"),
		Reason:     v.Reason,
		FilesCount: filesCount,
	}
	if v.Clinic.Valid {
		resp.Clinic = &v.Clinic.String
	}
	if v.Note.Valid {
		resp.Note = &v.Note.String
	}
	return resp
}

func validateCreateVetVisitRequest(req models.CreateVetVisitRequest) string {
	if req.VisitDate == "" || !isValidDateOnly(req.VisitDate) {
		return "Некорректный формат visit_date, ожидается YYYY-MM-DD"
	}
	if strings.TrimSpace(req.Reason) == "" || len(req.Reason) > models.VetVisitReasonMaxLen {
		return "Некорректное значение reason"
	}
	if req.Clinic != nil && len(*req.Clinic) > models.VetVisitClinicMaxLen {
		return "Поле clinic превышает допустимую длину"
	}
	if req.Note != nil && len(*req.Note) > models.VetVisitNoteMaxLen {
		return "Поле note превышает допустимую длину"
	}
	return ""
}

func GetPetVetVisitsHandler(w http.ResponseWriter, r *http.Request, petID uuid.UUID) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	limit, offset, ok := parsePetEventsPaging(r)
	if !ok {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Параметры limit/offset должны быть неотрицательными целыми числами")
		return
	}
	if _, ok := resolveOwnedPet(w, petID, userID); !ok {
		return
	}
	items, err := database.GetVetVisitsByPetID(petID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения визитов")
		return
	}
	ids := make([]uuid.UUID, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	filesCounts, err := database.CountFilesForOwners(vetVisitFileOwnerType, ids)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения количества файлов")
		return
	}
	resp := models.VetVisitListResponse{Items: make([]models.VetVisitResponse, 0, len(items))}
	for _, it := range items {
		resp.Items = append(resp.Items, vetVisitResponseFromDB(it, filesCounts[it.ID]))
	}
	writeJSON(w, http.StatusOK, resp)
}

func CreateVetVisitHandler(w http.ResponseWriter, r *http.Request, petID uuid.UUID) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	var req models.CreateVetVisitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректное тело запроса")
		return
	}
	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey != "" && !isValidUUIDv4(idempotencyKey) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Заголовок Idempotency-Key должен быть валидным UUID v4")
		return
	}
	if msg := validateCreateVetVisitRequest(req); msg != "" {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, msg)
		return
	}
	if !resolvePetForVetPassportCreate(w, petID, userID) {
		return
	}
	if replayVetPassportCreate(w, database.VetVisitTable, petID, idempotencyKey) {
		return
	}
	newID, err := database.InsertVetVisit(petID, req, idempotencyKey)
	if err != nil {
		if idempotencyKey != "" && database.IsUniqueViolation(err) && replayVetPassportCreate(w, database.VetVisitTable, petID, idempotencyKey) {
			return
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось создать визит")
		return
	}
	writeJSON(w, http.StatusCreated, models.IDResponse{ID: newID.String()})
}

func VetVisitByIDHandler(w http.ResponseWriter, r *http.Request) {
	id, err := parseIDFromPath(r, "/vet-visits/")
	if err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректный id визита")
		return
	}
	switch r.Method {
	case http.MethodPatch:
		UpdateVetVisitHandler(w, r, id)
	case http.MethodDelete:
		DeleteVetVisitHandler(w, r, id)
	default:
		writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
	}
}

func UpdateVetVisitHandler(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	visit, err := database.GetVetVisitByIDForUpdate(id)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Визит не найден")
			return
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения визита")
		return
	}
	belongs, err := database.CheckPetBelongsToUser(visit.PetID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка проверки прав доступа")
		return
	}
	if !belongs {
		writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Визит не найден")
		return
	}
	var req models.UpdateVetVisitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректное тело запроса")
		return
	}
	if req.VisitDate != nil && !isValidDateOnly(*req.VisitDate) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректный формат visit_date, ожидается YYYY-MM-DD")
		return
	}
	if req.Reason != nil && (strings.TrimSpace(*req.Reason) == "" || len(*req.Reason) > models.VetVisitReasonMaxLen) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректное значение reason")
		return
	}
	if req.Clinic != nil && len(*req.Clinic) > models.VetVisitClinicMaxLen {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поле clinic превышает допустимую длину")
		return
	}
	if req.Note != nil && len(*req.Note) > models.VetVisitNoteMaxLen {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поле note превышает допустимую длину")
		return
	}
	if err := database.UpdateVetVisit(id, req); err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка обновления визита")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func DeleteVetVisitHandler(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	visit, err := database.GetVetVisitByIDForUpdate(id)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Визит не найден")
			return
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения визита")
		return
	}
	belongs, err := database.CheckPetBelongsToUser(visit.PetID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка проверки прав доступа")
		return
	}
	if !belongs {
		writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Визит не найден")
		return
	}
	if err := database.SoftDeleteVetVisit(id); err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Визит не найден")
			return
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка удаления визита")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Allergy
// ---------------------------------------------------------------------------

func allergyResponseFromDB(a models.AllergyDB, filesCount int) models.AllergyResponse {
	resp := models.AllergyResponse{
		ID:         a.ID.String(),
		PetID:      a.PetID.String(),
		Allergen:   a.Allergen,
		Severity:   a.Severity,
		FilesCount: filesCount,
	}
	if a.Reaction.Valid {
		resp.Reaction = &a.Reaction.String
	}
	if a.DetectedDate.Valid {
		s := a.DetectedDate.Time.Format("2006-01-02")
		resp.DetectedDate = &s
	}
	if a.Note.Valid {
		resp.Note = &a.Note.String
	}
	return resp
}

func validateCreateAllergyRequest(req models.CreateAllergyRequest) string {
	if strings.TrimSpace(req.Allergen) == "" || len(req.Allergen) > models.AllergyAllergenMaxLen {
		return "Некорректное значение allergen"
	}
	if req.Reaction != nil && len(*req.Reaction) > models.AllergyReactionMaxLen {
		return "Поле reaction превышает допустимую длину"
	}
	if req.DetectedDate != nil && *req.DetectedDate != "" && !isValidDateOnly(*req.DetectedDate) {
		return "Некорректный формат detected_date, ожидается YYYY-MM-DD"
	}
	if !models.IsValidAllergySeverity(req.Severity) {
		return "Некорректное значение severity"
	}
	if req.Note != nil && len(*req.Note) > models.AllergyNoteMaxLen {
		return "Поле note превышает допустимую длину"
	}
	return ""
}

func GetPetAllergiesHandler(w http.ResponseWriter, r *http.Request, petID uuid.UUID) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	limit, offset, ok := parsePetEventsPaging(r)
	if !ok {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Параметры limit/offset должны быть неотрицательными целыми числами")
		return
	}
	if _, ok := resolveOwnedPet(w, petID, userID); !ok {
		return
	}
	items, err := database.GetAllergiesByPetID(petID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения аллергий")
		return
	}
	ids := make([]uuid.UUID, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	filesCounts, err := database.CountFilesForOwners(allergyFileOwnerType, ids)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения количества файлов")
		return
	}
	resp := models.AllergyListResponse{Items: make([]models.AllergyResponse, 0, len(items))}
	for _, it := range items {
		resp.Items = append(resp.Items, allergyResponseFromDB(it, filesCounts[it.ID]))
	}
	writeJSON(w, http.StatusOK, resp)
}

func CreateAllergyHandler(w http.ResponseWriter, r *http.Request, petID uuid.UUID) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	var req models.CreateAllergyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректное тело запроса")
		return
	}
	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey != "" && !isValidUUIDv4(idempotencyKey) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Заголовок Idempotency-Key должен быть валидным UUID v4")
		return
	}
	if msg := validateCreateAllergyRequest(req); msg != "" {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, msg)
		return
	}
	if !resolvePetForVetPassportCreate(w, petID, userID) {
		return
	}
	if replayVetPassportCreate(w, database.AllergyTable, petID, idempotencyKey) {
		return
	}
	newID, err := database.InsertAllergy(petID, req, idempotencyKey)
	if err != nil {
		if idempotencyKey != "" && database.IsUniqueViolation(err) && replayVetPassportCreate(w, database.AllergyTable, petID, idempotencyKey) {
			return
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось создать аллергию")
		return
	}
	writeJSON(w, http.StatusCreated, models.IDResponse{ID: newID.String()})
}

func AllergyByIDHandler(w http.ResponseWriter, r *http.Request) {
	id, err := parseIDFromPath(r, "/allergies/")
	if err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректный id аллергии")
		return
	}
	switch r.Method {
	case http.MethodPatch:
		UpdateAllergyHandler(w, r, id)
	case http.MethodDelete:
		DeleteAllergyHandler(w, r, id)
	default:
		writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
	}
}

func UpdateAllergyHandler(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	allergy, err := database.GetAllergyByIDForUpdate(id)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Аллергия не найдена")
			return
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения аллергии")
		return
	}
	belongs, err := database.CheckPetBelongsToUser(allergy.PetID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка проверки прав доступа")
		return
	}
	if !belongs {
		writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Аллергия не найдена")
		return
	}
	var req models.UpdateAllergyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректное тело запроса")
		return
	}
	if req.Allergen != nil && (strings.TrimSpace(*req.Allergen) == "" || len(*req.Allergen) > models.AllergyAllergenMaxLen) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректное значение allergen")
		return
	}
	if req.Reaction != nil && len(*req.Reaction) > models.AllergyReactionMaxLen {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поле reaction превышает допустимую длину")
		return
	}
	if req.DetectedDate != nil && *req.DetectedDate != "" && !isValidDateOnly(*req.DetectedDate) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректный формат detected_date, ожидается YYYY-MM-DD")
		return
	}
	if req.Severity != nil && !models.IsValidAllergySeverity(*req.Severity) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректное значение severity")
		return
	}
	if req.Note != nil && len(*req.Note) > models.AllergyNoteMaxLen {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поле note превышает допустимую длину")
		return
	}
	if err := database.UpdateAllergy(id, req); err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка обновления аллергии")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func DeleteAllergyHandler(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	allergy, err := database.GetAllergyByIDForUpdate(id)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Аллергия не найдена")
			return
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения аллергии")
		return
	}
	belongs, err := database.CheckPetBelongsToUser(allergy.PetID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка проверки прав доступа")
		return
	}
	if !belongs {
		writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Аллергия не найдена")
		return
	}
	if err := database.SoftDeleteAllergy(id); err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Аллергия не найдена")
			return
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка удаления аллергии")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Medication
// ---------------------------------------------------------------------------

// medicationResponseFromDB строит MedicationResponse, вычисляя next_dose по
// текущим полям расписания курса относительно now (см. "Вычисление
// next_dose"); даты и times расписания трактуются как местное время пояса
// loc (параметр tz запроса, по умолчанию UTC).
func medicationResponseFromDB(m models.MedicationDB, filesCount int, now time.Time, loc *time.Location) models.MedicationResponse {
	resp := models.MedicationResponse{
		ID:            m.ID.String(),
		PetID:         m.PetID.String(),
		Name:          m.Name,
		Dosage:        m.Dosage,
		FrequencyType: m.FrequencyType,
		FilesCount:    filesCount,
	}
	if m.ReminderPlanID.Valid {
		s := m.ReminderPlanID.UUID.String()
		resp.ReminderPlanID = &s
	}
	if m.Weekdays != nil {
		w := m.Weekdays
		resp.Weekdays = &w
	}
	if m.IntervalDays.Valid {
		v := int(m.IntervalDays.Int64)
		resp.IntervalDays = &v
	}
	if m.Times != nil {
		t := m.Times
		resp.Times = &t
	}
	var startDate time.Time
	if m.StartDate.Valid {
		s := m.StartDate.Time.Format("2006-01-02")
		resp.StartDate = &s
		startDate = m.StartDate.Time
	}
	var endDate *time.Time
	if m.EndDate.Valid {
		s := m.EndDate.Time.Format("2006-01-02")
		resp.EndDate = &s
		endDate = &m.EndDate.Time
	}
	if m.Note.Valid {
		resp.Note = &m.Note.String
	}

	intervalDays := 0
	if m.IntervalDays.Valid {
		intervalDays = int(m.IntervalDays.Int64)
	}
	if m.StartDate.Valid {
		if nextDose := computeMedicationNextDose(m.FrequencyType, m.Weekdays, intervalDays, m.Times, startDate, endDate, now, loc); nextDose != nil {
			s := nextDose.UTC().Format(time.RFC3339)
			resp.NextDose = &s
		}
	}
	return resp
}

// validateMedicationFieldsConsistency проверяет согласованность
// weekdays/interval_days/times/start_date/end_date с frequency_type — общая
// функция для создания (значения запроса как есть) и редактирования
// (эффективные, уже смёрженные с текущим состоянием значения), см.
// "Лекарства — Backend", раздел «Согласованность полей расписания с
// frequency_type».
func validateMedicationFieldsConsistency(freq string, weekdays []int, intervalDays *int, times []models.MedicationTimeSlot, startDate *string, endDate *string) string {
	if !models.IsValidMedicationFrequencyType(freq) {
		return "Некорректное значение frequency_type"
	}

	if freq == models.MedicationFrequencySpecificDays {
		if len(weekdays) < 1 || len(weekdays) > 7 {
			return "Поле weekdays обязательно и должно содержать 1-7 уникальных значений 1-7 при frequency_type=specific_days"
		}
		seen := map[int]bool{}
		for _, d := range weekdays {
			if d < models.MedicationMinWeekday || d > models.MedicationMaxWeekday || seen[d] {
				return "Поле weekdays должно содержать уникальные значения в диапазоне 1-7"
			}
			seen[d] = true
		}
	} else if weekdays != nil {
		return "Поле weekdays допустимо только при frequency_type=specific_days"
	}

	if freq == models.MedicationFrequencyEveryNDays {
		if intervalDays == nil || *intervalDays < models.MedicationMinIntervalDays || *intervalDays > models.MedicationMaxIntervalDays {
			return "Поле interval_days обязательно и должно быть в диапазоне 2-365 при frequency_type=every_n_days"
		}
	} else if intervalDays != nil {
		return "Поле interval_days допустимо только при frequency_type=every_n_days"
	}

	if freq != models.MedicationFrequencyAsNeeded {
		if len(times) < models.MedicationMinTimesCount || len(times) > models.MedicationMaxTimesCount {
			return "Поле times обязательно и должно содержать 1-4 элемента при frequency_type!=as_needed"
		}
		seenTimes := map[string]bool{}
		for _, t := range times {
			if !isValidTimeOfDay(t.Time) || seenTimes[t.Time] {
				return "Поле times содержит некорректное или повторяющееся время"
			}
			seenTimes[t.Time] = true
			if t.DoseNote != nil && len(*t.DoseNote) > models.MedicationDoseNoteMaxLen {
				return "Поле dose_note превышает допустимую длину"
			}
		}
		if startDate == nil || !isValidDateOnly(*startDate) {
			return "Некорректный формат start_date, ожидается YYYY-MM-DD"
		}
	} else {
		if times != nil {
			return "Поле times допустимо только при frequency_type!=as_needed"
		}
		if startDate != nil {
			return "Поле start_date допустимо только при frequency_type!=as_needed"
		}
	}

	if endDate != nil {
		if freq == models.MedicationFrequencyAsNeeded {
			return "Поле end_date недопустимо при frequency_type=as_needed"
		}
		if !isValidDateOnly(*endDate) {
			return "Некорректный формат end_date, ожидается YYYY-MM-DD"
		}
		if startDate != nil && *endDate < *startDate {
			return "Поле end_date не может быть раньше start_date"
		}
	}

	return ""
}

func validateCreateMedicationRequest(req models.CreateMedicationRequest) string {
	if strings.TrimSpace(req.Name) == "" || len(req.Name) > models.MedicationNameMaxLen {
		return "Некорректное значение name"
	}
	if strings.TrimSpace(req.Dosage) == "" || len(req.Dosage) > models.MedicationDosageMaxLen {
		return "Некорректное значение dosage"
	}
	if msg := validateMedicationFieldsConsistency(req.FrequencyType, req.Weekdays, req.IntervalDays, req.Times, req.StartDate, req.EndDate); msg != "" {
		return msg
	}
	if req.AddReminders != nil && *req.AddReminders && req.FrequencyType == models.MedicationFrequencyAsNeeded {
		return "Поле add_reminders недопустимо при frequency_type=as_needed"
	}
	if req.Note != nil && len(*req.Note) > models.MedicationNoteMaxLen {
		return "Поле note превышает допустимую длину"
	}
	return ""
}

func GetPetMedicationsHandler(w http.ResponseWriter, r *http.Request, petID uuid.UUID) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	limit, offset, ok := parsePetEventsPaging(r)
	if !ok {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Параметры limit/offset должны быть неотрицательными целыми числами")
		return
	}
	loc, ok := parseTimeZoneParam(w, r)
	if !ok {
		return
	}
	if _, ok := resolveOwnedPet(w, petID, userID); !ok {
		return
	}
	items, err := database.GetMedicationsByPetID(petID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения курсов лекарств")
		return
	}

	now := time.Now().UTC()
	ids := make([]uuid.UUID, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	filesCounts, err := database.CountFilesForOwners(medicationFileOwnerType, ids)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения количества файлов")
		return
	}

	responses := make([]models.MedicationResponse, 0, len(items))
	for _, it := range items {
		responses = append(responses, medicationResponseFromDB(it, filesCounts[it.ID], now, loc))
	}
	// Сортировка: по ближайшему будущему next_dose возр. (null — в конце),
	// затем по created_at убыв. — см. "Лекарства — Backend", раздел «Список».
	sort.SliceStable(responses, func(i, j int) bool {
		ni, nj := responses[i].NextDose, responses[j].NextDose
		if (ni == nil) != (nj == nil) {
			return ni != nil
		}
		if ni != nil && nj != nil && *ni != *nj {
			return *ni < *nj
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})

	if offset >= len(responses) {
		responses = []models.MedicationResponse{}
	} else {
		end := offset + limit
		if end > len(responses) {
			end = len(responses)
		}
		responses = responses[offset:end]
	}

	writeJSON(w, http.StatusOK, models.MedicationListResponse{Items: responses})
}

// medicationReminderSpec строит расписание набора напоминаний лекарства из
// его полей расписания: слоты несут дозу по времени приёма (dose_note) как
// собственную заметку напоминания. frequency_type лекарства не может быть
// as_needed (у такого лекарства нет расписания).
func medicationReminderSpec(frequencyType string, weekdays []int, intervalDays *int, times []models.MedicationTimeSlot, startDate time.Time, endDate *time.Time) reminderScheduleSpec {
	spec := reminderScheduleSpec{
		FrequencyType: frequencyType,
		Weekdays:      weekdays,
		StartDate:     startDate,
		EndDate:       endDate,
	}
	if intervalDays != nil {
		spec.IntervalDays = *intervalDays
	}
	for _, t := range times {
		slot := reminderTimeSlot{Time: normalizeTimeOfDay(t.Time)}
		if t.DoseNote != nil && *t.DoseNote != "" {
			note := *t.DoseNote
			slot.Notes = &note
		}
		spec.Times = append(spec.Times, slot)
	}
	return spec
}

// createMedicationReminderPlan рассчитывает расписание лекарства и создаёт
// набор напоминаний: настройки source=medication с type=medication,
// value={name}, notes=dosage и напоминания по моментам. ok=false, если
// расписание не даёт ни одного будущего момента — набор не создаётся.
func createMedicationReminderPlan(exec database.Executor, userID string, petID, medicationID uuid.UUID, name, dosage string, spec reminderScheduleSpec, loc *time.Location, tz string, now time.Time) (planID uuid.UUID, ok bool, err error) {
	moments := computeReminderMoments(spec, loc, now, nil, models.ReminderMaxMomentsPerOperation)
	if len(moments) == 0 {
		return uuid.Nil, false, nil
	}
	value, err := json.Marshal(map[string]string{"name": name})
	if err != nil {
		return uuid.Nil, false, err
	}
	ownerID, err := uuid.Parse(userID)
	if err != nil {
		return uuid.Nil, false, err
	}
	planID = uuid.New()
	plan := planDataFromRequest(models.ReminderPlanRequest{
		Type:  "medication",
		Value: value,
		Notes: &dosage,
	}, planID, ownerID, spec, tz)
	plan.Source = models.ReminderSourceMedication
	plan.SourceID = uuid.NullUUID{UUID: medicationID, Valid: true}
	if err := database.InsertReminderPlanWith(exec, plan, []uuid.UUID{petID}, moments); err != nil {
		return uuid.Nil, false, err
	}
	return planID, true, nil
}

// medicationSpecFromDB строит расписание набора напоминаний из строки
// лекарства. Не вызывается для frequency_type=as_needed.
func medicationSpecFromDB(m *models.MedicationDB) reminderScheduleSpec {
	var intervalDays *int
	if m.IntervalDays.Valid {
		v := int(m.IntervalDays.Int64)
		intervalDays = &v
	}
	var endDate *time.Time
	if m.EndDate.Valid {
		end := m.EndDate.Time
		endDate = &end
	}
	return medicationReminderSpec(m.FrequencyType, m.Weekdays, intervalDays, m.Times, m.StartDate.Time, endDate)
}

func CreateMedicationHandler(w http.ResponseWriter, r *http.Request, petID uuid.UUID) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	var req models.CreateMedicationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректное тело запроса")
		return
	}
	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey != "" && !isValidUUIDv4(idempotencyKey) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Заголовок Idempotency-Key должен быть валидным UUID v4")
		return
	}
	if msg := validateCreateMedicationRequest(req); msg != "" {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, msg)
		return
	}
	loc, ok := parseTimeZoneParam(w, r)
	if !ok {
		return
	}
	tz := r.URL.Query().Get("tz")
	if !resolvePetForVetPassportCreate(w, petID, userID) {
		return
	}
	if replayVetPassportCreate(w, database.MedicationTable, petID, idempotencyKey) {
		return
	}

	now := time.Now().UTC()
	var newID uuid.UUID
	err := database.RunInTx(func(tx *sql.Tx) error {
		id, err := database.InsertMedicationWith(tx, petID, req, idempotencyKey)
		if err != nil {
			return err
		}
		newID = id

		// Если расписание не даёт ни одного будущего момента (например,
		// end_date в прошлом), набор не создаётся, лекарство сохраняется без
		// напоминаний — это не ошибка.
		if req.AddReminders == nil || !*req.AddReminders {
			return nil
		}
		startDate, _ := parseDateOnly(*req.StartDate)
		var endDate *time.Time
		if req.EndDate != nil {
			t, _ := parseDateOnly(*req.EndDate)
			endDate = &t
		}
		spec := medicationReminderSpec(req.FrequencyType, req.Weekdays, req.IntervalDays, req.Times, startDate, endDate)
		planID, created, err := createMedicationReminderPlan(tx, userID, petID, newID, req.Name, req.Dosage, spec, loc, tz, now)
		if err != nil {
			return err
		}
		if !created {
			return nil
		}
		return database.SetMedicationReminderPlanIDWith(tx, newID, uuid.NullUUID{UUID: planID, Valid: true})
	})
	if err != nil {
		if idempotencyKey != "" && database.IsUniqueViolation(err) && replayVetPassportCreate(w, database.MedicationTable, petID, idempotencyKey) {
			return
		}
		writeVetPassportTxError(w, err, "Курс лекарств не найден", "Не удалось создать курс лекарств")
		return
	}

	writeJSON(w, http.StatusCreated, models.IDResponse{ID: newID.String()})
}

// MedicationByIDHandler обрабатывает /medications/{id} (PATCH/DELETE) и
// /medications/{id}/reminders (POST/DELETE).
func MedicationByIDHandler(w http.ResponseWriter, r *http.Request) {
	segments := pathSegments(r, "/medications/")
	if len(segments) == 0 {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректный id курса лекарств")
		return
	}
	id, err := uuid.Parse(segments[0])
	if err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректный id курса лекарств")
		return
	}

	if len(segments) == 2 && segments[1] == "reminders" {
		switch r.Method {
		case http.MethodPost:
			CreateMedicationRemindersHandler(w, r, id)
		case http.MethodDelete:
			DeleteMedicationRemindersHandler(w, r, id)
		default:
			writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
		}
		return
	}

	if len(segments) != 1 {
		writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Не найдено")
		return
	}

	switch r.Method {
	case http.MethodPatch:
		UpdateMedicationHandler(w, r, id)
	case http.MethodDelete:
		DeleteMedicationHandler(w, r, id)
	default:
		writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
	}
}

func equalIntSlices(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalTimeSlots(a, b []models.MedicationTimeSlot) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Time != b[i].Time {
			return false
		}
		an, bn := a[i].DoseNote, b[i].DoseNote
		if (an == nil) != (bn == nil) {
			return false
		}
		if an != nil && *an != *bn {
			return false
		}
	}
	return true
}

func equalStringPtr(a, b *string) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	return a == nil || *a == *b
}

func equalIntPtr(a, b *int) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	return a == nil || *a == *b
}

func nullIntToPtr(v sql.NullInt64) *int {
	if !v.Valid {
		return nil
	}
	i := int(v.Int64)
	return &i
}

func nullTimeToDateStrPtr(v sql.NullTime) *string {
	if !v.Valid {
		return nil
	}
	s := v.Time.Format("2006-01-02")
	return &s
}

// UpdateMedicationHandler обрабатывает PATCH /medications/{id} — см.
// "Лекарства — Backend", раздел «Редактирование»: смёрживает переданные
// поля с текущим состоянием, валидирует итоговую согласованность с
// frequency_type, и, если поля расписания изменились и у лекарства уже есть
// набор напоминаний, либо оставляет набор как есть
// (regenerate_reminders=false), либо пересоздаёт будущие напоминания
// (regenerate_reminders=true) — переход в as_needed при наличии набора без
// regenerate_reminders=true запрещён (400). name и dosage применяются к
// настройкам набора в той же транзакции.
func UpdateMedicationHandler(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	medication, err := database.GetMedicationByIDForUpdate(id)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Курс лекарств не найден")
			return
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения курса лекарств")
		return
	}
	belongs, err := database.CheckPetBelongsToUser(medication.PetID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка проверки прав доступа")
		return
	}
	if !belongs {
		writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Курс лекарств не найден")
		return
	}
	var req models.UpdateMedicationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректное тело запроса")
		return
	}
	if req.Name != nil && (strings.TrimSpace(*req.Name) == "" || len(*req.Name) > models.MedicationNameMaxLen) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректное значение name")
		return
	}
	if req.Dosage != nil && (strings.TrimSpace(*req.Dosage) == "" || len(*req.Dosage) > models.MedicationDosageMaxLen) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректное значение dosage")
		return
	}
	if req.FrequencyType != nil && !models.IsValidMedicationFrequencyType(*req.FrequencyType) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректное значение frequency_type")
		return
	}
	if req.Note != nil && len(*req.Note) > models.MedicationNoteMaxLen {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поле note превышает допустимую длину")
		return
	}
	loc, ok := parseTimeZoneParam(w, r)
	if !ok {
		return
	}
	tz := r.URL.Query().Get("tz")

	// Эффективные (смёрженные с текущим состоянием лекарства) значения полей
	// расписания + признак того, что каждое поле реально изменилось.
	effFreq := medication.FrequencyType
	freqChanged := false
	if req.FrequencyType != nil {
		effFreq = *req.FrequencyType
		freqChanged = effFreq != medication.FrequencyType
	}

	effWeekdays := medication.Weekdays
	weekdaysChanged := false
	if req.Weekdays.Set {
		var newWeekdays []int
		if req.Weekdays.Value != nil {
			newWeekdays = *req.Weekdays.Value
		}
		weekdaysChanged = !equalIntSlices(medication.Weekdays, newWeekdays)
		effWeekdays = newWeekdays
	}

	effIntervalDays := nullIntToPtr(medication.IntervalDays)
	intervalChanged := false
	if req.IntervalDays.Set {
		intervalChanged = !equalIntPtr(effIntervalDays, req.IntervalDays.Value)
		effIntervalDays = req.IntervalDays.Value
	}

	effTimes := medication.Times
	timesChanged := false
	if req.Times.Set {
		var newTimes []models.MedicationTimeSlot
		if req.Times.Value != nil {
			newTimes = *req.Times.Value
		}
		timesChanged = !equalTimeSlots(medication.Times, newTimes)
		effTimes = newTimes
	}

	effStartDate := nullTimeToDateStrPtr(medication.StartDate)
	startDateChanged := false
	if req.StartDate.Set {
		startDateChanged = !equalStringPtr(effStartDate, req.StartDate.Value)
		effStartDate = req.StartDate.Value
	}

	effEndDate := nullTimeToDateStrPtr(medication.EndDate)
	endDateChanged := false
	if req.EndDate.Set {
		endDateChanged = !equalStringPtr(effEndDate, req.EndDate.Value)
		effEndDate = req.EndDate.Value
	}

	if msg := validateMedicationFieldsConsistency(effFreq, effWeekdays, effIntervalDays, effTimes, effStartDate, effEndDate); msg != "" {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, msg)
		return
	}

	scheduleFieldsChanged := freqChanged || weekdaysChanged || intervalChanged || timesChanged || startDateChanged || endDateChanged
	hasPlan := medication.ReminderPlanID.Valid
	regenerate := req.RegenerateReminders != nil && *req.RegenerateReminders

	if scheduleFieldsChanged && hasPlan && !regenerate && effFreq == models.MedicationFrequencyAsNeeded {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Переход в frequency_type=as_needed при наличии набора напоминаний требует regenerate_reminders=true")
		return
	}

	effName := medication.Name
	if req.Name != nil {
		effName = *req.Name
	}
	effDosage := medication.Dosage
	if req.Dosage != nil {
		effDosage = *req.Dosage
	}

	now := time.Now().UTC()
	var orphanKeys []string
	err = database.RunInTx(func(tx *sql.Tx) error {
		locked, err := database.GetMedicationByIDWith(tx, id, true)
		if err != nil {
			return err
		}
		if err := database.UpdateMedicationWith(tx, id, req); err != nil {
			return err
		}
		if !locked.ReminderPlanID.Valid {
			return nil
		}
		plan, err := database.GetReminderPlanForUserWith(tx, locked.ReminderPlanID.UUID, userID, true)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}

		update := database.ReminderPlanUpdate{}
		// name и dosage применяются к настройкам набора; напоминания не
		// пересоздаются, а доза по времени приёма остаётся у своих
		// напоминаний.
		if req.Name != nil {
			value, err := json.Marshal(map[string]string{"name": effName})
			if err != nil {
				return err
			}
			raw := json.RawMessage(value)
			update.Value = &raw
		}
		if req.Dosage != nil {
			update.Notes = &effDosage
		}

		if scheduleFieldsChanged && regenerate {
			if effFreq == models.MedicationFrequencyAsNeeded {
				// as_needed не может сосуществовать с набором напоминаний:
				// настройки удаляются, новых не создаётся.
				keys, err := database.DeleteReminderPlanWith(tx, plan.ID)
				if err != nil {
					return err
				}
				orphanKeys = keys
				return nil
			}

			startDate, _ := parseDateOnly(*effStartDate)
			var endDate *time.Time
			if effEndDate != nil {
				t, _ := parseDateOnly(*effEndDate)
				endDate = &t
			}
			spec := medicationReminderSpec(effFreq, effWeekdays, effIntervalDays, effTimes, startDate, endDate)
			closed, err := database.ListClosedRemindMomentsWith(tx, plan.ID)
			if err != nil {
				return err
			}
			moments := computeReminderMoments(spec, loc, now, closed, models.ReminderMaxMomentsPerOperation)
			if len(moments) == 0 {
				return newReminderHTTPError(http.StatusBadRequest, openapi.VALIDATIONERROR, "Расписание не даёт ни одного момента в будущем")
			}
			keys, err := database.DeleteFutureOpenRemindersWith(tx, plan.ID, now)
			if err != nil {
				return err
			}
			orphanKeys = keys
			if _, err := database.InsertRemindersWith(tx, plan.ID, moments); err != nil {
				return err
			}
			update.Schedule = planScheduleFromSpec(spec, tz)
		}
		return database.UpdateReminderPlanWith(tx, plan.ID, update)
	})
	if err != nil {
		writeVetPassportTxError(w, err, "Курс лекарств не найден", "Ошибка обновления курса лекарств")
		return
	}
	deleteOrphanedObjects(r.Context(), orphanKeys)

	w.WriteHeader(http.StatusNoContent)
}

// DeleteMedicationHandler обрабатывает DELETE /medications/{id}: мягко
// удаляет запись лекарства и жёстко удаляет настройки набора напоминаний со
// всеми напоминаниями (см. "Исключение из правила soft-delete").
func DeleteMedicationHandler(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	medication, ok := resolveOwnedMedicationForReminders(w, id, userID)
	if !ok {
		return
	}

	var orphanKeys []string
	err := database.RunInTx(func(tx *sql.Tx) error {
		if err := database.SoftDeleteMedicationWith(tx, id); err != nil {
			return err
		}
		if !medication.ReminderPlanID.Valid {
			return nil
		}
		keys, err := database.DeleteReminderPlanWith(tx, medication.ReminderPlanID.UUID)
		orphanKeys = keys
		return err
	})
	if err != nil {
		writeVetPassportTxError(w, err, "Курс лекарств не найден", "Ошибка удаления курса лекарств")
		return
	}
	deleteOrphanedObjects(r.Context(), orphanKeys)

	w.WriteHeader(http.StatusNoContent)
}

// resolveOwnedMedicationForReminders находит лекарство по id и проверяет
// владение через его питомца — общий шаг DELETE /medications/{id} и
// POST/DELETE /medications/{id}/reminders.
func resolveOwnedMedicationForReminders(w http.ResponseWriter, id uuid.UUID, userID string) (*models.MedicationDB, bool) {
	medication, err := database.GetMedicationByIDForUpdate(id)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Курс лекарств не найден")
			return nil, false
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения курса лекарств")
		return nil, false
	}
	belongs, err := database.CheckPetBelongsToUser(medication.PetID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка проверки прав доступа")
		return nil, false
	}
	if !belongs {
		writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Курс лекарств не найден")
		return nil, false
	}
	return medication, true
}

// CreateMedicationRemindersHandler обрабатывает
// POST /medications/{id}/reminders: создаёт набор напоминаний по расписанию,
// вычисленному из текущих полей расписания лекарства (не больше 60
// напоминаний). Доступно только если набора ещё нет (иначе 409) и
// frequency_type != as_needed (иначе 400); если будущих моментов нет — 400 —
// см. "Лекарства — Backend", раздел «Ручное создание набора напоминаний».
func CreateMedicationRemindersHandler(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	loc, ok := parseTimeZoneParam(w, r)
	if !ok {
		return
	}
	tz := r.URL.Query().Get("tz")
	medication, ok := resolveOwnedMedicationForReminders(w, id, userID)
	if !ok {
		return
	}

	err := database.RunInTx(func(tx *sql.Tx) error {
		locked, err := database.GetMedicationByIDWith(tx, id, true)
		if err != nil {
			return err
		}
		if locked.ReminderPlanID.Valid {
			return newReminderHTTPError(http.StatusConflict, openapi.CONFLICT, "У курса лекарств уже есть набор напоминаний — сначала удалите его")
		}
		if locked.FrequencyType == models.MedicationFrequencyAsNeeded {
			return newReminderHTTPError(http.StatusBadRequest, openapi.VALIDATIONERROR, "У курса лекарств с frequency_type=as_needed нет расписания")
		}
		planID, created, err := createMedicationReminderPlan(tx, userID, locked.PetID, locked.ID, locked.Name, locked.Dosage, medicationSpecFromDB(locked), loc, tz, time.Now().UTC())
		if err != nil {
			return err
		}
		if !created {
			return newReminderHTTPError(http.StatusBadRequest, openapi.VALIDATIONERROR, "Расписание не даёт ни одного момента в будущем")
		}
		return database.SetMedicationReminderPlanIDWith(tx, id, uuid.NullUUID{UUID: planID, Valid: true})
	})
	if err != nil {
		writeVetPassportTxError(w, err, "Курс лекарств не найден", "Не удалось создать набор напоминаний")
		return
	}

	medication, err = database.GetMedicationByIDForUpdate(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения курса лекарств")
		return
	}
	filesCounts, err := database.CountFilesForOwners(medicationFileOwnerType, []uuid.UUID{medication.ID})
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения количества файлов")
		return
	}

	writeJSON(w, http.StatusOK, medicationResponseFromDB(*medication, filesCounts[medication.ID], time.Now().UTC(), loc))
}

// DeleteMedicationRemindersHandler обрабатывает
// DELETE /medications/{id}/reminders — жёстко удаляет настройки набора
// напоминаний со всеми напоминаниями (исключение из soft-delete). Требует,
// чтобы набор был (иначе 404).
func DeleteMedicationRemindersHandler(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	if _, ok := resolveOwnedMedicationForReminders(w, id, userID); !ok {
		return
	}

	var orphanKeys []string
	err := database.RunInTx(func(tx *sql.Tx) error {
		locked, err := database.GetMedicationByIDWith(tx, id, true)
		if err != nil {
			return err
		}
		if !locked.ReminderPlanID.Valid {
			return newReminderHTTPError(http.StatusNotFound, openapi.NOTFOUND, "У курса лекарств нет набора напоминаний")
		}
		keys, err := database.DeleteReminderPlanWith(tx, locked.ReminderPlanID.UUID)
		orphanKeys = keys
		return err
	})
	if err != nil {
		writeVetPassportTxError(w, err, "Курс лекарств не найден", "Не удалось удалить набор напоминаний")
		return
	}
	deleteOrphanedObjects(r.Context(), orphanKeys)

	w.WriteHeader(http.StatusNoContent)
}

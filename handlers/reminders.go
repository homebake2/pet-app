// Package handlers: напоминания — настройки напоминания (reminder_plan) и
// напоминания по моментам расписания (reminder). Эндпоинты A–J описаны в
// «Напоминания — Backend»: создание, чтение и изменение настроек, удаление
// всех напоминаний или одного, отметка прошедшего, замена одного новой
// записью, ближайшие напоминания (для уведомлений на устройстве и виджета).
//
// Каждая составная операция выполняется в одной транзакции БД; S3-объекты,
// на которые не осталось ссылок, удаляются после фиксации транзакции.
package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"myauthservice/database"
	"myauthservice/eventreg"
	"myauthservice/models"
	"myauthservice/openapi"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
)

const (
	// reminderPlanFileOwnerType/reminderFileOwnerType — owner_type файлов
	// настроек напоминания и напоминания в реестре владельцев (см.
	// handlers/files.go).
	reminderPlanFileOwnerType = database.ReminderPlanFileOwnerType
	reminderFileOwnerType     = database.ReminderFileOwnerType
)

// reminderHTTPError — ошибка проверки, которую надо превратить в конкретный
// HTTP-статус; внутри транзакции она откатывает транзакцию.
type reminderHTTPError struct {
	status  int
	code    openapi.ErrorCodeEnum
	message string
}

func (e *reminderHTTPError) Error() string { return e.message }

func newReminderHTTPError(status int, code openapi.ErrorCodeEnum, message string) error {
	return &reminderHTTPError{status: status, code: code, message: message}
}

// writeReminderTxError отвечает на ошибку транзакции: reminderHTTPError —
// своим статусом, sql.ErrNoRows — 404, остальное — 500.
func writeReminderTxError(w http.ResponseWriter, err error, internalMessage string) {
	var httpErr *reminderHTTPError
	if errors.As(err, &httpErr) {
		writeError(w, httpErr.status, httpErr.code, httpErr.message)
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Не найдено")
		return
	}
	writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, internalMessage)
}

// deleteOrphanedObjects удаляет из S3 объекты, на которые не осталось ссылок
// (best-effort, после фиксации транзакции).
func deleteOrphanedObjects(ctx context.Context, objectKeys []string) {
	for _, key := range objectKeys {
		bestEffortDeleteObject(ctx, key)
	}
}

// ---------------------------------------------------------------------------
// Общие проверки данных настроек (те же правила, что у POST /events).
// ---------------------------------------------------------------------------

// validateReminderPlanData проверяет type, value и notes настроек теми же
// правилами, что POST /events: type из справочника, форма value по type,
// длина notes. Пустая строка — данные корректны.
func validateReminderPlanData(eventType string, value json.RawMessage, notes *string) string {
	if !eventreg.IsValidType(eventType) {
		return "Некорректное значение type"
	}
	if msg := validateEventValue(eventType, value); msg != "" {
		return msg
	}
	if !validateNotesLength(notes) {
		return "Поле notes не должно превышать 500 символов"
	}
	return ""
}

// validateReminderPlanDataForSpecies проверяет применимость type и значений
// вложенных словарей value к виду питомца. Пустая строка — данные допустимы.
func validateReminderPlanDataForSpecies(eventType string, value json.RawMessage, species string) string {
	if !isTypeApplicableToPet(eventType, species) {
		return "Тип события " + eventType + " неприменим к виду питомца"
	}
	return isNestedValueApplicableToPet(eventType, value, species)
}

func nullStringFromPtr(s *string) sql.NullString {
	if s == nil || *s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: *s, Valid: true}
}

// planDataFromRequest собирает строку reminder_plan из проверенного запроса
// создания и разобранного расписания.
func planDataFromRequest(req models.ReminderPlanRequest, id, petID uuid.UUID, spec reminderScheduleSpec, tz string) models.ReminderPlanDB {
	plan := models.ReminderPlanDB{
		ID:            id,
		PetID:         petID,
		Source:        models.ReminderSourceManual,
		Type:          req.Type,
		Value:         req.Value,
		Notes:         nullStringFromPtr(req.Notes),
		FrequencyType: spec.FrequencyType,
		Weekdays:      spec.Weekdays,
		Times:         reminderPlanTimes(spec),
		StartDate:     spec.StartDate,
		TZ:            tz,
	}
	if spec.FrequencyType == models.ReminderFrequencyEveryNDays {
		plan.IntervalDays = sql.NullInt64{Int64: int64(spec.IntervalDays), Valid: true}
	}
	if spec.EndDate != nil {
		plan.EndDate = sql.NullTime{Time: *spec.EndDate, Valid: true}
	}
	return plan
}

// planScheduleFromSpec строит запись нового расписания настроек.
func planScheduleFromSpec(spec reminderScheduleSpec, tz string) *database.ReminderPlanSchedule {
	schedule := &database.ReminderPlanSchedule{
		FrequencyType: spec.FrequencyType,
		Weekdays:      spec.Weekdays,
		Times:         reminderPlanTimes(spec),
		StartDate:     spec.StartDate,
		TZ:            tz,
	}
	if spec.FrequencyType == models.ReminderFrequencyEveryNDays {
		schedule.IntervalDays = sql.NullInt64{Int64: int64(spec.IntervalDays), Valid: true}
	}
	if spec.EndDate != nil {
		schedule.EndDate = sql.NullTime{Time: *spec.EndDate, Valid: true}
	}
	return schedule
}

// preparedReminderPlan — проверенный запрос создания настроек: разобранное
// расписание и рассчитанные моменты.
type preparedReminderPlan struct {
	id      uuid.UUID
	petID   uuid.UUID
	req     models.ReminderPlanRequest
	spec    reminderScheduleSpec
	tz      string
	moments []models.ReminderMoment
}

// prepareReminderPlanCreate проверяет запрос создания настроек (формат
// полей, расписание, питомец, type и value) и рассчитывает моменты. tz —
// имя пояса из запроса. Возвращает reminderHTTPError с нужным статусом.
func prepareReminderPlanCreate(userID string, req models.ReminderPlanRequest, tz string, loc *time.Location, now time.Time) (*preparedReminderPlan, error) {
	bad := func(msg string) error {
		return newReminderHTTPError(http.StatusBadRequest, openapi.VALIDATIONERROR, msg)
	}

	planID, err := uuid.Parse(req.ID)
	if err != nil {
		return nil, bad("Некорректный id настроек (ожидается UUID)")
	}
	if req.PetID == "" || req.Type == "" || len(req.Value) == 0 || req.FrequencyType == "" || req.StartDate == "" {
		return nil, bad("Обязательные поля не заполнены")
	}
	petID, err := uuid.Parse(req.PetID)
	if err != nil {
		return nil, bad("Некорректный ID питомца")
	}
	if msg := validateReminderPlanData(req.Type, req.Value, req.Notes); msg != "" {
		return nil, bad(msg)
	}
	spec, msg := parseReminderSchedule(reminderScheduleInput{
		FrequencyType: req.FrequencyType,
		Weekdays:      req.Weekdays,
		IntervalDays:  req.IntervalDays,
		Times:         req.Times,
		StartDate:     req.StartDate,
		EndDate:       req.EndDate,
	})
	if msg != "" {
		return nil, bad(msg)
	}

	petDB, err := database.GetPetIdDBByIDAndUserID(petID, userID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, newReminderHTTPError(http.StatusNotFound, openapi.NOTFOUND, "Питомец "+req.PetID+" не найден")
		}
		return nil, err
	}
	if petDB.DeletedAt.Valid {
		// Мягко удалённый питомец для настроек не существует — 404, как и
		// на остальных эндпоинтах напоминаний.
		return nil, newReminderHTTPError(http.StatusNotFound, openapi.NOTFOUND, "Питомец "+req.PetID+" не найден")
	}
	if msg := validateReminderPlanDataForSpecies(req.Type, req.Value, petDB.Species); msg != "" {
		return nil, bad(msg)
	}

	moments := computeReminderMoments(spec, loc, now, nil, models.ReminderMaxMomentsPerOperation)
	if len(moments) == 0 {
		return nil, bad("Расписание не даёт ни одного момента в будущем")
	}

	return &preparedReminderPlan{id: planID, petID: petID, req: req, spec: spec, tz: tz, moments: moments}, nil
}

// insertPreparedReminderPlan вставляет настройки и напоминания (источник
// manual).
func insertPreparedReminderPlan(exec database.Executor, p *preparedReminderPlan) error {
	return database.InsertReminderPlanWith(exec, planDataFromRequest(p.req, p.id, p.petID, p.spec, p.tz), p.moments)
}

// ---------------------------------------------------------------------------
// Ответы.
// ---------------------------------------------------------------------------

// buildReminderPlanResponse строит ReminderPlanResponse по настройкам:
// незавершённые напоминания, файлы настроек (с presigned-ссылками) и число
// будущих напоминаний с собственными файлами.
func buildReminderPlanResponse(r *http.Request, plan *database.ReminderPlanFull, now time.Time) (models.ReminderPlanResponse, error) {
	open, err := database.ListOpenRemindersWith(database.DB, plan.ID)
	if err != nil {
		return models.ReminderPlanResponse{}, err
	}
	files, err := database.GetFilesForOwnerWith(database.DB, reminderPlanFileOwnerType, plan.ID)
	if err != nil {
		return models.ReminderPlanResponse{}, err
	}
	fileItems, err := presignFileItems(r, files)
	if err != nil {
		return models.ReminderPlanResponse{}, err
	}
	futureWithFiles, err := database.CountFutureRemindersWithOwnFilesWith(database.DB, plan.ID, now)
	if err != nil {
		return models.ReminderPlanResponse{}, err
	}

	resp := models.ReminderPlanResponse{
		ID:            plan.ID.String(),
		PetID:         plan.PetID.String(),
		PetName:       plan.PetName,
		Type:          plan.Type,
		Value:         plan.Value,
		FrequencyType: plan.FrequencyType,
		Times:         plan.Times,
		StartDate:     plan.StartDate.Format("2006-01-02"),
		Source:        plan.Source,
		Reminders:     make([]models.ReminderRef, 0, len(open)),
		Files:         fileItems,

		FutureRemindersWithFilesCount: futureWithFiles,
	}
	if plan.Notes.Valid {
		resp.Notes = &plan.Notes.String
	}
	if plan.Weekdays != nil {
		weekdays := plan.Weekdays
		resp.Weekdays = &weekdays
	}
	if plan.IntervalDays.Valid {
		v := int(plan.IntervalDays.Int64)
		resp.IntervalDays = &v
	}
	if plan.EndDate.Valid {
		s := plan.EndDate.Time.Format("2006-01-02")
		resp.EndDate = &s
	}
	if plan.SourceID.Valid {
		s := plan.SourceID.UUID.String()
		resp.SourceID = &s
	}
	if plan.SourceTitle.Valid {
		resp.SourceTitle = &plan.SourceTitle.String
	}
	for _, reminder := range open {
		resp.Reminders = append(resp.Reminders, models.ReminderRef{
			ID:       reminder.ID.String(),
			RemindAt: reminder.RemindAt.UTC().Format(time.RFC3339),
		})
	}
	return resp, nil
}

// writeReminderPlanByID читает настройки заново (после фиксации транзакции)
// и отвечает ими со статусом status.
func writeReminderPlanByID(w http.ResponseWriter, r *http.Request, status int, planID uuid.UUID, userID string) {
	plan, err := database.GetReminderPlanForUserWith(database.DB, planID, userID, false)
	if err != nil {
		writeReminderTxError(w, err, "Ошибка получения настроек напоминания")
		return
	}
	resp, err := buildReminderPlanResponse(r, plan, time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения настроек напоминания")
		return
	}
	writeJSON(w, status, resp)
}

// ---------------------------------------------------------------------------
// Маршрутизация.
// ---------------------------------------------------------------------------

// ReminderPlansHandler обрабатывает POST /reminder-plans.
func ReminderPlansHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
		return
	}
	CreateReminderPlanHandler(w, r)
}

// ReminderPlanByIDHandler обрабатывает /reminder-plans/{id}: GET, PATCH,
// DELETE.
func ReminderPlanByIDHandler(w http.ResponseWriter, r *http.Request) {
	segments := pathSegments(r, "/reminder-plans/")
	if len(segments) != 1 {
		writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Не найдено")
		return
	}
	planID, err := uuid.Parse(segments[0])
	if err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректный id настроек напоминания")
		return
	}
	switch r.Method {
	case http.MethodGet:
		GetReminderPlanHandler(w, r, planID)
	case http.MethodPatch:
		UpdateReminderPlanHandler(w, r, planID)
	case http.MethodDelete:
		DeleteReminderPlanHandler(w, r, planID)
	default:
		writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
	}
}

// RemindersUpcomingHandler обрабатывает GET /reminders/upcoming.
func RemindersUpcomingHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
		return
	}
	GetUpcomingRemindersHandler(w, r)
}

// ReminderByIDHandler обрабатывает /reminders/{id} (GET, DELETE),
// /reminders/{id}/complete и /reminders/{id}/detach (POST).
func ReminderByIDHandler(w http.ResponseWriter, r *http.Request) {
	segments := pathSegments(r, "/reminders/")
	if len(segments) == 0 || len(segments) > 2 {
		writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Не найдено")
		return
	}
	reminderID, err := uuid.Parse(segments[0])
	if err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректный id напоминания")
		return
	}

	if len(segments) == 2 {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
			return
		}
		switch segments[1] {
		case "complete":
			CompleteReminderHandler(w, r, reminderID)
		case "detach":
			DetachReminderHandler(w, r, reminderID)
		default:
			writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Не найдено")
		}
		return
	}

	switch r.Method {
	case http.MethodGet:
		GetReminderHandler(w, r, reminderID)
	case http.MethodDelete:
		DeleteReminderHandler(w, r, reminderID)
	default:
		writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
	}
}

// ---------------------------------------------------------------------------
// A. POST /reminder-plans
// ---------------------------------------------------------------------------

// CreateReminderPlanHandler обрабатывает POST /reminder-plans — создаёт
// настройки (source = manual) и напоминания по моментам расписания.
// Клиентский id заменяет Idempotency-Key.
func CreateReminderPlanHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var req models.ReminderPlanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректное тело запроса")
		return
	}
	loc, ok := parseTimeZoneParam(w, r)
	if !ok {
		return
	}
	tz := r.URL.Query().Get("tz")

	// Повтор создания: настройки с таким id уже существуют — инициатору
	// возвращаются они же, чужие — 409.
	if planID, err := uuid.Parse(req.ID); err == nil {
		if handled := replayReminderPlanCreate(w, r, planID, userID); handled {
			return
		}
	}

	prepared, err := prepareReminderPlanCreate(userID, req, tz, loc, time.Now().UTC())
	if err != nil {
		writeReminderTxError(w, err, "Ошибка создания настроек напоминания")
		return
	}

	err = database.RunInTx(func(tx *sql.Tx) error {
		return insertPreparedReminderPlan(tx, prepared)
	})
	if err != nil {
		if database.IsUniqueViolation(err) {
			// Гонка параллельных запросов с одним клиентским id.
			if handled := replayReminderPlanCreate(w, r, prepared.id, userID); handled {
				return
			}
		}
		writeReminderTxError(w, err, "Не удалось создать настройки напоминания")
		return
	}

	writeReminderPlanByID(w, r, http.StatusCreated, prepared.id, userID)
}

// replayReminderPlanCreate отвечает на повтор создания: свои настройки с
// таким id — 201 с теми же данными, чужие — 409. handled=false, если
// настроек с таким id нет (либо при ошибке, которая уже записана в ответ —
// тогда handled=true).
func replayReminderPlanCreate(w http.ResponseWriter, r *http.Request, planID uuid.UUID, userID string) (handled bool) {
	ownerID, err := database.GetReminderPlanOwnerUserID(planID)
	if err == sql.ErrNoRows {
		return false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка проверки настроек напоминания")
		return true
	}
	if ownerID != userID {
		writeError(w, http.StatusConflict, openapi.CONFLICT, "Идентификатор настроек уже занят")
		return true
	}
	writeReminderPlanByID(w, r, http.StatusCreated, planID, userID)
	return true
}

// ---------------------------------------------------------------------------
// B. GET /reminder-plans/{id}
// ---------------------------------------------------------------------------

// GetReminderPlanHandler обрабатывает GET /reminder-plans/{id}.
func GetReminderPlanHandler(w http.ResponseWriter, r *http.Request, planID uuid.UUID) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	writeReminderPlanByID(w, r, http.StatusOK, planID, userID)
}

// ---------------------------------------------------------------------------
// C. PATCH /reminder-plans/{id}
// ---------------------------------------------------------------------------

// UpdateReminderPlanHandler обрабатывает PATCH /reminder-plans/{id} —
// «Изменить все напоминания»: type/value/notes и/или расписание целиком.
func UpdateReminderPlanHandler(w http.ResponseWriter, r *http.Request, planID uuid.UUID) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var req models.UpdateReminderPlanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректное тело запроса")
		return
	}
	loc, ok := parseTimeZoneParam(w, r)
	if !ok {
		return
	}
	tz := r.URL.Query().Get("tz")

	scheduleGiven := req.FrequencyType != nil || req.Weekdays != nil || req.IntervalDays != nil ||
		req.Times != nil || req.StartDate != nil || req.EndDate.Set
	dataGiven := req.Type != nil || req.Value != nil || req.Notes != nil
	if !scheduleGiven && !dataGiven {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Необходимо указать хотя бы одно поле для обновления")
		return
	}
	if req.Type != nil && req.Value == nil {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "При смене type поле value обязательно в этом же запросе")
		return
	}
	if req.Value != nil && len(*req.Value) == 0 {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поле value не должно быть пустым")
		return
	}
	if !validateNotesLength(req.Notes) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поле notes не должно превышать 500 символов")
		return
	}

	var spec reminderScheduleSpec
	if scheduleGiven {
		if req.FrequencyType == nil || req.Times == nil || req.StartDate == nil {
			writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поля расписания передаются только целиком: frequency_type, times и start_date обязательны")
			return
		}
		input := reminderScheduleInput{
			FrequencyType: *req.FrequencyType,
			Times:         *req.Times,
			StartDate:     *req.StartDate,
		}
		if req.Weekdays != nil {
			input.Weekdays = *req.Weekdays
		}
		input.IntervalDays = req.IntervalDays
		if req.EndDate.Set {
			input.EndDate = req.EndDate.Value
		}
		var msg string
		spec, msg = parseReminderSchedule(input)
		if msg != "" {
			writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, msg)
			return
		}
	}

	now := time.Now().UTC()
	var orphanKeys []string
	err := database.RunInTx(func(tx *sql.Tx) error {
		plan, err := database.GetReminderPlanForUserWith(tx, planID, userID, true)
		if err != nil {
			return err
		}
		bad := func(msg string) error {
			return newReminderHTTPError(http.StatusBadRequest, openapi.VALIDATIONERROR, msg)
		}

		linked := plan.Source != models.ReminderSourceManual
		if linked && dataGiven {
			return bad("Тип, значение и заметка настроек, связанных с записью Ветпаспорта, меняются только через неё")
		}
		if scheduleGiven {
			if plan.Source == models.ReminderSourceMedication && spec.FrequencyType == models.ReminderFrequencyOnce {
				return bad("Расписание лекарства не может быть разовым")
			}
			if plan.Source == models.ReminderSourceVaccination && spec.FrequencyType != models.ReminderFrequencyOnce {
				return bad("Напоминание вакцинации допускает только разовое расписание")
			}
		}

		// Тип, значение и применимость к виду питомца — теми же правилами,
		// что при создании.
		effectiveType := plan.Type
		if req.Type != nil {
			effectiveType = *req.Type
		}
		if req.Type != nil || req.Value != nil {
			if !eventreg.IsValidType(effectiveType) {
				return bad("Некорректное значение type")
			}
			value := plan.Value
			if req.Value != nil {
				value = *req.Value
			}
			if msg := validateEventValue(effectiveType, value); msg != "" {
				return bad(msg)
			}
			petDB, err := database.GetPetIdDBByIDAndUserID(plan.PetID, userID)
			if err != nil {
				return err
			}
			if msg := validateReminderPlanDataForSpecies(effectiveType, value, petDB.Species); msg != "" {
				return bad(msg)
			}
		}

		update := database.ReminderPlanUpdate{Type: req.Type, Value: req.Value, Notes: req.Notes}

		if scheduleGiven && !sameReminderSchedule(spec, plan.ReminderPlanDB, tz) {
			closed, err := database.ListClosedRemindMomentsWith(tx, planID)
			if err != nil {
				return err
			}
			// Времена лекарства несут дозу: у времени, оставшегося в списке,
			// сохраняется его dose_note, у добавленного она пуста.
			var med *models.MedicationDB
			if plan.Source == models.ReminderSourceMedication && plan.SourceID.Valid {
				med, err = database.GetMedicationByIDWith(tx, plan.SourceID.UUID, true)
				if err != nil && err != sql.ErrNoRows {
					return err
				}
			}
			if med != nil {
				for i, slot := range spec.Times {
					for _, medSlot := range med.Times {
						if normalizeTimeOfDay(medSlot.Time) == slot.Time && medSlot.DoseNote != nil && *medSlot.DoseNote != "" {
							note := *medSlot.DoseNote
							spec.Times[i].Notes = &note
						}
					}
				}
			}

			moments := computeReminderMoments(spec, loc, now, closed, models.ReminderMaxMomentsPerOperation)
			if len(moments) == 0 {
				return bad("Расписание не даёт ни одного момента в будущем")
			}

			orphanKeys, err = database.DeleteFutureOpenRemindersWith(tx, planID, now)
			if err != nil {
				return err
			}
			if _, err := database.InsertRemindersWith(tx, planID, moments); err != nil {
				return err
			}
			update.Schedule = planScheduleFromSpec(spec, tz)

			if err := applyScheduleToPlanOwner(tx, plan.ReminderPlanDB, med, spec); err != nil {
				return err
			}
		}

		return database.UpdateReminderPlanWith(tx, planID, update)
	})
	if err != nil {
		writeReminderTxError(w, err, "Не удалось изменить настройки напоминания")
		return
	}
	deleteOrphanedObjects(r.Context(), orphanKeys)

	w.WriteHeader(http.StatusNoContent)
}

// applyScheduleToPlanOwner применяет новое расписание настроек к записи
// владельца в той же транзакции: лекарство получает поля расписания и
// времена с сохранёнными дозами, прививка — дату следующей вакцинации.
func applyScheduleToPlanOwner(tx *sql.Tx, plan models.ReminderPlanDB, med *models.MedicationDB, spec reminderScheduleSpec) error {
	switch plan.Source {
	case models.ReminderSourceMedication:
		if med == nil {
			return nil
		}
		slots := make([]models.MedicationTimeSlot, 0, len(spec.Times))
		for _, t := range spec.Times {
			slot := models.MedicationTimeSlot{Time: t.Time, DoseNote: t.Notes}
			slots = append(slots, slot)
		}
		freq := spec.FrequencyType
		startDate := spec.StartDate.Format("2006-01-02")
		req := models.UpdateMedicationRequest{
			FrequencyType: &freq,
			Weekdays:      models.OptionalField[[]int]{Set: true},
			IntervalDays:  models.OptionalField[int]{Set: true},
			Times:         models.OptionalField[[]models.MedicationTimeSlot]{Set: true, Value: &slots},
			StartDate:     models.OptionalField[string]{Set: true, Value: &startDate},
			EndDate:       models.OptionalField[string]{Set: true},
		}
		if spec.Weekdays != nil {
			weekdays := spec.Weekdays
			req.Weekdays.Value = &weekdays
		}
		if spec.FrequencyType == models.ReminderFrequencyEveryNDays {
			interval := spec.IntervalDays
			req.IntervalDays.Value = &interval
		}
		if spec.EndDate != nil {
			endDate := spec.EndDate.Format("2006-01-02")
			req.EndDate.Value = &endDate
		}
		return database.UpdateMedicationWith(tx, med.ID, req)
	case models.ReminderSourceVaccination:
		if !plan.SourceID.Valid {
			return nil
		}
		startDate := spec.StartDate
		return database.SetVaccinationNextDateWith(tx, plan.SourceID.UUID, &startDate)
	}
	return nil
}

// ---------------------------------------------------------------------------
// D. DELETE /reminder-plans/{id}
// ---------------------------------------------------------------------------

// DeleteReminderPlanHandler обрабатывает DELETE /reminder-plans/{id} —
// жёстко удаляет настройки, все их напоминания и файлы.
func DeleteReminderPlanHandler(w http.ResponseWriter, r *http.Request, planID uuid.UUID) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var orphanKeys []string
	err := database.RunInTx(func(tx *sql.Tx) error {
		if _, err := database.GetReminderPlanForUserWith(tx, planID, userID, true); err != nil {
			return err
		}
		var err error
		orphanKeys, err = database.DeleteReminderPlanWith(tx, planID)
		return err
	})
	if err != nil {
		writeReminderTxError(w, err, "Не удалось удалить настройки напоминания")
		return
	}
	deleteOrphanedObjects(r.Context(), orphanKeys)

	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// E. GET /reminders/{id}
// ---------------------------------------------------------------------------

// GetReminderHandler обрабатывает GET /reminders/{id} — одно незавершённое
// напоминание с данными его настроек (форма «Изменить это»).
func GetReminderHandler(w http.ResponseWriter, r *http.Request, reminderID uuid.UUID) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	full, err := database.GetReminderForUserWith(database.DB, reminderID, userID, false)
	if err != nil {
		writeReminderTxError(w, err, "Ошибка получения напоминания")
		return
	}
	if full.Reminder.ClosedAt.Valid {
		writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Напоминание не найдено")
		return
	}

	planFiles, err := database.GetFilesForOwnerWith(database.DB, reminderPlanFileOwnerType, full.Plan.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения файлов напоминания")
		return
	}
	ownFiles, err := database.GetFilesForOwnerWith(database.DB, reminderFileOwnerType, full.Reminder.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения файлов напоминания")
		return
	}
	planFileItems, err := presignFileItems(r, planFiles)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось подписать ссылку на файл напоминания")
		return
	}
	ownFileItems, err := presignFileItems(r, ownFiles)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось подписать ссылку на файл напоминания")
		return
	}
	unclosed, err := database.CountOpenRemindersWith(database.DB, full.Plan.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения напоминания")
		return
	}

	resp := models.ReminderResponse{
		ID:                full.Reminder.ID.String(),
		PlanID:            full.Plan.ID.String(),
		RemindAt:          full.Reminder.RemindAt.UTC().Format(time.RFC3339),
		Type:              full.Plan.Type,
		Value:             full.Plan.Value,
		Notes:             effectiveReminderNotes(full.Reminder.Notes, full.Plan.Notes),
		PetID:             full.Plan.PetID.String(),
		PetName:           full.PetName,
		PlanSource:        full.Plan.Source,
		PlanFiles:         planFileItems,
		Files:             ownFileItems,
		PlanUnclosedCount: unclosed,
	}
	if full.SourceTitle.Valid {
		resp.PlanSourceTitle = &full.SourceTitle.String
	}
	writeJSON(w, http.StatusOK, resp)
}

// ---------------------------------------------------------------------------
// F. DELETE /reminders/{id}
// ---------------------------------------------------------------------------

// DeleteReminderHandler обрабатывает DELETE /reminders/{id} — закрывает
// напоминание без факта (close_reason = deleted) и удаляет его собственные
// файлы; опустевшие настройки удаляются.
func DeleteReminderHandler(w http.ResponseWriter, r *http.Request, reminderID uuid.UUID) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var orphanKeys []string
	err := database.RunInTx(func(tx *sql.Tx) error {
		full, err := database.GetReminderForUserWith(tx, reminderID, userID, true)
		if err != nil {
			return err
		}
		if full.Reminder.ClosedAt.Valid {
			return sql.ErrNoRows
		}
		orphanKeys, _, err = database.CloseReminderWith(tx, full.Reminder, models.ReminderCloseDeleted, uuid.NullUUID{}, time.Now().UTC())
		return err
	})
	if err != nil {
		writeReminderTxError(w, err, "Не удалось удалить напоминание")
		return
	}
	deleteOrphanedObjects(r.Context(), orphanKeys)

	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// G. POST /reminders/{id}/complete
// ---------------------------------------------------------------------------

// CompleteReminderHandler обрабатывает POST /reminders/{id}/complete —
// закрывает прошедшее напоминание как «выполнено» (создаётся факт) либо «не
// выполнено».
func CompleteReminderHandler(w http.ResponseWriter, r *http.Request, reminderID uuid.UUID) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var req models.CompleteReminderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректное тело запроса")
		return
	}
	if req.Done == nil {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поле done обязательно")
		return
	}
	done := *req.Done

	var factEventID uuid.NullUUID
	var orphanKeys []string
	err := database.RunInTx(func(tx *sql.Tx) error {
		full, err := database.GetReminderForUserWith(tx, reminderID, userID, true)
		if err != nil {
			return err
		}
		reminder := full.Reminder

		// Повторный вызов: напоминание уже закрыто как выполненное либо
		// невыполненное — тот же ответ, ничего не создаётся. Закрытое как
		// удалённое либо заменённое — 404.
		if reminder.ClosedAt.Valid {
			reason := reminder.CloseReason.String
			if reason == models.ReminderCloseDone || reason == models.ReminderCloseSkipped {
				factEventID = reminder.FactEventID
				return nil
			}
			return sql.ErrNoRows
		}

		now := time.Now().UTC()
		if reminder.RemindAt.After(now) {
			return newReminderHTTPError(http.StatusConflict, openapi.CONFLICT, "Напоминание ещё не наступило")
		}

		reason := models.ReminderCloseSkipped
		if done {
			reason = models.ReminderCloseDone
			notes := effectiveReminderNotes(reminder.Notes, full.Plan.Notes)
			eventID, err := database.InsertEventWith(tx, full.Plan.PetID, models.CreateEventRequest{
				PetID: full.Plan.PetID.String(),
				Date:  reminder.RemindAt.UTC().Format(time.RFC3339),
				Type:  full.Plan.Type,
				Notes: notes,
				Value: full.Plan.Value,
			}, "")
			if err != nil {
				return err
			}
			if err := database.CopyEventFilesFromReminderWith(tx, userID, full.Plan.ID, reminder.ID, eventID); err != nil {
				return err
			}
			factEventID = uuid.NullUUID{UUID: eventID, Valid: true}
		}

		var closeErr error
		orphanKeys, _, closeErr = database.CloseReminderWith(tx, reminder, reason, factEventID, now)
		if closeErr != nil {
			return closeErr
		}

		// Отметка «выполнено» напоминания на следующую вакцинацию очищает
		// next_date вакцинации: она перестаёт быть «просроченной».
		if done && full.Plan.Source == models.ReminderSourceVaccination && full.Plan.SourceID.Valid {
			return database.SetVaccinationNextDateWith(tx, full.Plan.SourceID.UUID, nil)
		}
		return nil
	})
	if err != nil {
		writeReminderTxError(w, err, "Не удалось закрыть напоминание")
		return
	}
	deleteOrphanedObjects(r.Context(), orphanKeys)

	resp := models.CompleteReminderResponse{}
	if factEventID.Valid {
		s := factEventID.UUID.String()
		resp.FactEventID = &s
	}
	writeJSON(w, http.StatusOK, resp)
}

// ---------------------------------------------------------------------------
// H. POST /reminders/{id}/detach
// ---------------------------------------------------------------------------

// DetachReminderHandler обрабатывает POST /reminders/{id}/detach — заменяет
// одно напоминание новой независимой записью: фактом либо новыми
// настройками со своим расписанием. Исходное напоминание закрывается без
// факта (close_reason = replaced).
func DetachReminderHandler(w http.ResponseWriter, r *http.Request, reminderID uuid.UUID) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey == "" || !isValidUUIDv4(idempotencyKey) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Заголовок Idempotency-Key обязателен и должен быть валидным UUID v4")
		return
	}

	var req models.DetachReminderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректное тело запроса")
		return
	}
	if (req.Event == nil) == (req.Plan == nil) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Необходимо передать ровно одно из полей event и plan")
		return
	}

	var loc *time.Location
	tz := r.URL.Query().Get("tz")
	if req.Plan != nil {
		var ok bool
		loc, ok = parseTimeZoneParam(w, r)
		if !ok {
			return
		}
	}

	now := time.Now().UTC()

	// Данные новой записи проверяются до транзакции (формат полей,
	// расписание, виды питомца).
	var preparedEvent *preparedDetachEvent
	var preparedPlan *preparedReminderPlan

	// Напоминание, которого уже нет (повтор после успешного выполнения):
	// факт с таким Idempotency-Key либо настройки с таким id возвращаются
	// вместо 404.
	existing, err := database.GetReminderForUserWith(database.DB, reminderID, userID, false)
	if err != nil && err != sql.ErrNoRows {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения напоминания")
		return
	}
	if err == sql.ErrNoRows || existing.Reminder.ClosedAt.Valid {
		if replayDetach(w, r, req, idempotencyKey, userID) {
			return
		}
		writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Напоминание не найдено")
		return
	}

	if req.Event != nil {
		preparedEvent, err = prepareDetachEvent(userID, *req.Event, existing.Plan.PetID, now)
	} else {
		preparedPlan, err = prepareReminderPlanCreate(userID, *req.Plan, tz, loc, now)
		if err == nil && preparedPlan.petID != existing.Plan.PetID {
			err = newReminderHTTPError(http.StatusBadRequest, openapi.VALIDATIONERROR, "Перенос напоминания между питомцами не поддерживается")
		}
	}
	if err != nil {
		writeReminderTxError(w, err, "Не удалось заменить напоминание")
		return
	}

	var newEventID, newPlanID uuid.UUID
	var orphanKeys []string
	err = database.RunInTx(func(tx *sql.Tx) error {
		full, err := database.GetReminderForUserWith(tx, reminderID, userID, true)
		if err != nil {
			return err
		}
		if full.Reminder.ClosedAt.Valid {
			return sql.ErrNoRows
		}
		if full.Plan.Source == models.ReminderSourceVaccination {
			return newReminderHTTPError(http.StatusConflict, openapi.CONFLICT, "Замена напоминания вакцинации недоступна: измените его через «Изменить все»")
		}
		newType := ""
		if preparedEvent != nil {
			newType = preparedEvent.req.Type
		} else {
			newType = preparedPlan.req.Type
		}
		if full.Plan.Source == models.ReminderSourceMedication && newType != full.Plan.Type {
			return newReminderHTTPError(http.StatusBadRequest, openapi.VALIDATIONERROR, "Тип новой записи должен совпадать с типом напоминания лекарства")
		}

		if preparedEvent != nil {
			newEventID, err = database.InsertEventWith(tx, full.Plan.PetID, preparedEvent.req, idempotencyKey)
			if err != nil {
				return err
			}
			if err := database.CopyEventFilesFromReminderWith(tx, userID, full.Plan.ID, full.Reminder.ID, newEventID); err != nil {
				return err
			}
		} else {
			if err := insertPreparedReminderPlan(tx, preparedPlan); err != nil {
				return err
			}
			newPlanID = preparedPlan.id
			if err := database.CopyPlanFilesFromReminderWith(tx, userID, full.Plan.ID, full.Reminder.ID, newPlanID); err != nil {
				return err
			}
		}

		orphanKeys, _, err = database.CloseReminderWith(tx, full.Reminder, models.ReminderCloseReplaced, uuid.NullUUID{}, now)
		return err
	})
	if err != nil {
		if database.IsUniqueViolation(err) {
			// Гонка повторов с одним ключом либо занятый чужими настройками
			// id.
			if replayDetach(w, r, req, idempotencyKey, userID) {
				return
			}
			writeError(w, http.StatusConflict, openapi.CONFLICT, "Идентификатор настроек уже занят")
			return
		}
		writeReminderTxError(w, err, "Не удалось заменить напоминание")
		return
	}
	deleteOrphanedObjects(r.Context(), orphanKeys)

	if preparedEvent != nil {
		writeDetachEventResponse(w, r, newEventID)
		return
	}
	plan, err := database.GetReminderPlanForUserWith(database.DB, newPlanID, userID, false)
	if err != nil {
		writeReminderTxError(w, err, "Ошибка получения настроек напоминания")
		return
	}
	resp, err := buildReminderPlanResponse(r, plan, time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения настроек напоминания")
		return
	}
	writeJSON(w, http.StatusCreated, models.DetachReminderResponse{Plan: &resp})
}

// preparedDetachEvent — проверенное тело event запроса замены.
type preparedDetachEvent struct {
	req models.CreateEventRequest
}

// prepareDetachEvent проверяет тело event запроса замены теми же правилами,
// что POST /events (включая дату факта) и совпадение питомца с питомцем
// заменяемого напоминания.
func prepareDetachEvent(userID string, req models.CreateEventRequest, reminderPetID uuid.UUID, now time.Time) (*preparedDetachEvent, error) {
	bad := func(msg string) error {
		return newReminderHTTPError(http.StatusBadRequest, openapi.VALIDATIONERROR, msg)
	}
	if req.PetID == "" || req.Date == "" || req.Type == "" || len(req.Value) == 0 {
		return nil, bad("Обязательные поля не заполнены")
	}
	if !eventreg.IsValidType(req.Type) {
		return nil, bad("Некорректное значение type")
	}
	date, err := parseEventDate(req.Date)
	if err != nil {
		return nil, bad("Некорректный формат даты")
	}
	if msg := validateEventValue(req.Type, req.Value); msg != "" {
		return nil, bad(msg)
	}
	if !validateNotesLength(req.Notes) {
		return nil, bad("Поле notes не должно превышать 500 символов")
	}
	if msg := validateFactDate(date); msg != "" {
		return nil, bad(msg)
	}
	petID, err := uuid.Parse(req.PetID)
	if err != nil {
		return nil, bad("Некорректный ID питомца")
	}
	if petID != reminderPetID {
		return nil, bad("Перенос напоминания между питомцами не поддерживается")
	}
	petDB, err := database.GetPetIdDBByIDAndUserID(petID, userID)
	if err != nil {
		return nil, err
	}
	if msg := validateReminderPlanDataForSpecies(req.Type, req.Value, petDB.Species); msg != "" {
		return nil, bad(msg)
	}
	return &preparedDetachEvent{req: req}, nil
}

// replayDetach отвечает на повтор замены после успешного выполнения:
// возвращает ранее созданный факт с тем же Idempotency-Key (для питомца из
// тела) либо настройки с тем же клиентским id. handled=false, если ничего
// не найдено.
func replayDetach(w http.ResponseWriter, r *http.Request, req models.DetachReminderRequest, idempotencyKey, userID string) (handled bool) {
	if req.Event != nil {
		petID, err := uuid.Parse(req.Event.PetID)
		if err != nil {
			return false
		}
		if _, err := database.GetPetIdDBByIDAndUserID(petID, userID); err != nil {
			return false
		}
		eventDB, eventPetID, petName, err := database.GetEventByPetIDAndIdempotencyKey(petID, idempotencyKey)
		if err != nil {
			return false
		}
		response, ok := buildEventResponse(w, r, eventDB, eventPetID, petName)
		if !ok {
			return true
		}
		writeJSON(w, http.StatusCreated, models.DetachReminderResponse{Event: &response})
		return true
	}

	planID, err := uuid.Parse(req.Plan.ID)
	if err != nil {
		return false
	}
	plan, err := database.GetReminderPlanForUserWith(database.DB, planID, userID, false)
	if err != nil {
		return false
	}
	resp, err := buildReminderPlanResponse(r, plan, time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения настроек напоминания")
		return true
	}
	writeJSON(w, http.StatusCreated, models.DetachReminderResponse{Plan: &resp})
	return true
}

// writeDetachEventResponse читает созданный факт и отвечает 201 с ним.
func writeDetachEventResponse(w http.ResponseWriter, r *http.Request, eventID uuid.UUID) {
	eventDB, petID, petName, err := database.GetEventByID(eventID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Событие создано, но не удалось получить его данные")
		return
	}
	response, ok := buildEventResponse(w, r, eventDB, petID, petName)
	if !ok {
		return
	}
	writeJSON(w, http.StatusCreated, models.DetachReminderResponse{Event: &response})
}

// ---------------------------------------------------------------------------
// I, J. Ближайшие напоминания.
// ---------------------------------------------------------------------------

// parseUpcomingLimit разбирает необязательный query-параметр limit: целое
// 1..max, по умолчанию def.
func parseUpcomingLimit(r *http.Request, def, max int) (int, bool) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return def, true
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 1 || v > max {
		return 0, false
	}
	return v, true
}

// GetUpcomingRemindersHandler обрабатывает GET /reminders/upcoming —
// ближайшие незавершённые напоминания (remind_at >= now) по всем не мягко
// удалённым питомцам пользователя, для уведомлений на устройстве.
func GetUpcomingRemindersHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	limit, ok := parseUpcomingLimit(r, models.ReminderUpcomingDefaultLimit, models.ReminderUpcomingMaxLimit)
	if !ok {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Параметр limit должен быть целым числом от 1 до 64")
		return
	}

	rows, err := database.GetUpcomingRemindersByUserID(userID, time.Now().UTC(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения напоминаний")
		return
	}
	items := make([]models.UpcomingReminderItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, models.UpcomingReminderItem{
			ID:       row.ID.String(),
			PlanID:   row.PlanID.String(),
			RemindAt: row.RemindAt.UTC().Format(time.RFC3339),
			PetID:    row.PetID.String(),
			PetName:  row.PetName,
			Type:     row.Type,
		})
	}
	writeJSON(w, http.StatusOK, models.UpcomingRemindersResponse{Items: items})
}

// GetPetUpcomingRemindersHandler обрабатывает
// GET /pet/{id}/reminders/upcoming — ближайшие незавершённые напоминания
// одного питомца (для виджета).
func GetPetUpcomingRemindersHandler(w http.ResponseWriter, r *http.Request, petID uuid.UUID) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	limit, ok := parseUpcomingLimit(r, models.PetReminderUpcomingDefaultLimit, models.PetReminderUpcomingMaxLimit)
	if !ok {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Параметр limit должен быть целым числом от 1 до 10")
		return
	}
	if _, ok := resolveOwnedPet(w, petID, userID); !ok {
		return
	}

	rows, err := database.GetUpcomingRemindersByPetID(userID, petID, time.Now().UTC(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения напоминаний")
		return
	}
	items := make([]models.PetUpcomingReminderItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, models.PetUpcomingReminderItem{
			ID:       row.ID.String(),
			PlanID:   row.PlanID.String(),
			RemindAt: row.RemindAt.UTC().Format(time.RFC3339),
			Type:     row.Type,
		})
	}
	writeJSON(w, http.StatusOK, models.PetUpcomingRemindersResponse{Items: items})
}

package handlers

import (
	"database/sql"
	"encoding/json"
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
	maxActivitiesRange = 366 * 24 * time.Hour
	maxEventFieldLen   = 500
)

// parseEventDateRange разбирает границы периода from/to (YYYY-MM-DD, UTC) —
// общий код для GET /activities и GET /events/stats: трактовка границ и
// ограничение в 366 дней у них одни и те же (см. "Просмотр календаря —
// Backend" и "Графики динамики — Backend"). При ошибке сама пишет 400 и
// возвращает ok=false.
//
// Возвращаются календарные даты (полночь UTC); полуоткрытый интервал
// [from, to+1 день) строится потребителем.
func parseEventDateRange(w http.ResponseWriter, r *http.Request) (fromDate, toDate time.Time, ok bool) {
	fromStr := r.URL.Query().Get("from")
	if fromStr == "" {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Отсутствует обязательный параметр from")
		return time.Time{}, time.Time{}, false
	}

	toStr := r.URL.Query().Get("to")
	if toStr == "" {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Отсутствует обязательный параметр to")
		return time.Time{}, time.Time{}, false
	}

	fromDate, err := time.Parse("2006-01-02", fromStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректный формат даты from (ожидается YYYY-MM-DD)")
		return time.Time{}, time.Time{}, false
	}

	toDate, err = time.Parse("2006-01-02", toStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректный формат даты to (ожидается YYYY-MM-DD)")
		return time.Time{}, time.Time{}, false
	}

	if toDate.Before(fromDate) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Параметр from не может быть позже to")
		return time.Time{}, time.Time{}, false
	}

	if toDate.Sub(fromDate) > maxActivitiesRange {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Диапазон дат не должен превышать 366 дней")
		return time.Time{}, time.Time{}, false
	}

	return fromDate, toDate, true
}

// parseRequiredPetIDParam разбирает обязательный query-параметр pet_id —
// общий код для GET /activities и GET /events/stats.
func parseRequiredPetIDParam(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	petIDStr := r.URL.Query().Get("pet_id")
	if petIDStr == "" {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Отсутствует обязательный параметр pet_id")
		return uuid.Nil, false
	}

	petID, err := uuid.Parse(petIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректный формат pet_id")
		return uuid.Nil, false
	}

	return petID, true
}

// GetActivitiesHandler обрабатывает GET /activities
func GetActivitiesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
		return
	}

	fromDate, toDate, ok := parseEventDateRange(w, r)
	if !ok {
		return
	}

	petID, ok := parseRequiredPetIDParam(w, r)
	if !ok {
		return
	}

	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	belongs, err := database.CheckPetBelongsToUser(petID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка проверки принадлежности питомца")
		return
	}

	if !belongs {
		writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Питомец не найден")
		return
	}

	petName, err := database.GetPetNameByID(petID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения имени питомца")
		return
	}

	eventsDB, err := database.GetEventsByPetIDAndDateRange(petID, fromDate, toDate)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения событий")
		return
	}

	eventIDs := make([]uuid.UUID, len(eventsDB))
	for i, eventDB := range eventsDB {
		eventIDs[i] = eventDB.ID
	}
	filesCounts, err := database.CountFilesForOwners(eventFileOwnerType, eventIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения количества файлов событий")
		return
	}

	eventsByDay := make(map[string][]models.ActivityEvent)
	for _, eventDB := range eventsDB {
		// Календарный день события для группировки определяется по UTC-
		// представлению date_time, а не по location, в которой драйвер
		// вернул time.Time (см. "Просмотр календаря — Backend").
		eventDate := eventDB.Date.UTC()
		dateStr := eventDate.Format("2006-01-02")
		var notes *string
		if eventDB.Notes.Valid {
			notes = new(string)
			*notes = eventDB.Notes.String
		}
		activityEvent := models.ActivityEvent{
			ID:         eventDB.ID.String(),
			Date:       eventDate.Format(time.RFC3339),
			Type:       eventDB.Type,
			Notes:      notes,
			Value:      eventDB.Value,
			FilesCount: filesCounts[eventDB.ID],
		}
		eventsByDay[dateStr] = append(eventsByDay[dateStr], activityEvent)
	}

	var items []models.ActivityDay
	currentDate := fromDate
	for currentDate.Before(toDate.AddDate(0, 0, 1)) {
		dateStr := currentDate.Format("2006-01-02")
		events, exists := eventsByDay[dateStr]
		if !exists {
			events = []models.ActivityEvent{}
		}
		items = append(items, models.ActivityDay{
			Date:   dateStr,
			Events: events,
		})
		currentDate = currentDate.AddDate(0, 0, 1)
	}

	response := models.ActivitiesResponse{
		PetName: petName,
		Items:   items,
	}

	writeJSON(w, http.StatusOK, response)
}

// PetEventItem - одно событие в ответе GET /pet/{id}/events (GetEventResponse).
type PetEventItem struct {
	ID         string          `json:"id"`
	Date       string          `json:"date"`
	Type       string          `json:"type"`
	Notes      *string         `json:"notes,omitempty"`
	Value      json.RawMessage `json:"value"`
	FilesCount int             `json:"files_count"`
}

// PetEventsResponse - тело ответа GET /pet/{id}/events.
type PetEventsResponse struct {
	Items []PetEventItem `json:"items"`
}

const (
	defaultPetEventsLimit = 50
	maxPetEventsLimit     = 200
)

// parsePetEventsPaging разбирает query-параметры limit/offset для
// GET /pet/{id}/events. limit по умолчанию 50, молча ограничивается 200;
// offset по умолчанию 0. Если значение передано, но не парсится как целое
// число или отрицательно — возвращает ok=false (сервер должен ответить 400).
func parsePetEventsPaging(r *http.Request) (limit, offset int, ok bool) {
	limit = defaultPetEventsLimit
	offset = 0

	if raw := r.URL.Query().Get("limit"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 0 {
			return 0, 0, false
		}
		limit = v
	}
	if limit > maxPetEventsLimit {
		limit = maxPetEventsLimit
	}

	if raw := r.URL.Query().Get("offset"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 0 {
			return 0, 0, false
		}
		offset = v
	}

	return limit, offset, true
}

// GET /pet/{id}/events возвращает плоский список событий питомца (сортировка
// date_time DESC, пагинация limit/offset) — задел на будущий сценарий вида
// «вся история питомца». На момент написания клиентом не используется.
func GetPetEventsHandler(w http.ResponseWriter, r *http.Request, petID uuid.UUID) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	limit, offset, ok := parsePetEventsPaging(r)
	if !ok {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Параметры limit/offset должны быть неотрицательными целыми числами")
		return
	}

	// Питомец должен существовать, не быть удалён (deleted_at IS NULL) и
	// принадлежать userID — то же условие, что и в GET /pet/{id}. Иначе
	// события мягко удалённого/чужого/несуществующего питомца "утекают".
	if _, ok := resolveOwnedPet(w, petID, userID); !ok {
		return
	}

	eventsDB, err := database.GetEventsByPetID(petID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения событий")
		return
	}

	eventIDs := make([]uuid.UUID, len(eventsDB))
	for i, eventDB := range eventsDB {
		eventIDs[i] = eventDB.ID
	}
	filesCounts, err := database.CountFilesForOwners(eventFileOwnerType, eventIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения количества файлов событий")
		return
	}

	items := make([]PetEventItem, 0, len(eventsDB))
	for _, eventDB := range eventsDB {
		var notes *string
		if eventDB.Notes.Valid {
			notes = &eventDB.Notes.String
		}
		items = append(items, PetEventItem{
			ID:         eventDB.ID.String(),
			Date:       eventDB.Date.Format(time.RFC3339),
			Type:       eventDB.Type,
			Notes:      notes,
			Value:      eventDB.Value,
			FilesCount: filesCounts[eventDB.ID],
		})
	}

	writeJSON(w, http.StatusOK, PetEventsResponse{Items: items})
}

// EventIDResponseHandler обрабатывает /events/{id}: получение, частичное
// обновление (PATCH) и удаление события.
func EventIDResponseHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		GetEventHandler(w, r)
	case http.MethodPatch:
		UpdateEventHandler(w, r)
	case http.MethodDelete:
		DeleteEventHandler(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
	}
}

// DELETE /events/{id}
func DeleteEventHandler(w http.ResponseWriter, r *http.Request) {
	eventID, err := parseEventIDFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректный ID события")
		return
	}

	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	if _, _, _, ok := resolveOwnedEvent(w, eventID, userID); !ok {
		return
	}

	if err := database.DeleteEvent(eventID); err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Событие не найдено")
			return
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка удаления события")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// GET /events/{id}
func GetEventHandler(w http.ResponseWriter, r *http.Request) {
	eventID, err := parseEventIDFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректный ID события")
		return
	}

	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	eventDB, petID, petName, ok := resolveOwnedEvent(w, eventID, userID)
	if !ok {
		return
	}

	writeEventResponse(w, r, http.StatusOK, eventDB, petID, petName)
}

// POST /events
func CreateEventHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
		return
	}

	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var req models.CreateEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректное тело запроса")
		return
	}

	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey != "" && !isValidUUIDv4(idempotencyKey) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Заголовок Idempotency-Key должен быть валидным UUID v4")
		return
	}

	if req.PetID == "" || req.Date == "" || req.Type == "" || len(req.Value) == 0 {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Обязательные поля не заполнены")
		return
	}

	if !eventreg.IsValidType(req.Type) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректное значение type")
		return
	}

	if _, err := parseEventDate(req.Date); err != nil {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректный формат даты")
		return
	}

	if msg := validateEventValue(req.Type, req.Value); msg != "" {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, msg)
		return
	}

	if !validateNotesLength(req.Notes) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поле notes не должно превышать 500 символов")
		return
	}

	petID, err := uuid.Parse(req.PetID)
	if err != nil {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректный ID питомца")
		return
	}

	petDB, err := database.GetPetIdDBByIDAndUserID(petID, userID)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Питомец "+req.PetID+" не найден")
			return
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения питомца")
		return
	}

	if petDB.DeletedAt.Valid {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Невозможно создать запись о событии, так как питомец удален")
		return
	}

	if idempotencyKey != "" {
		if existing, existingPetID, existingPetName, err := database.GetEventByPetIDAndIdempotencyKey(petID, idempotencyKey); err == nil {
			writeEventResponse(w, r, http.StatusCreated, existing, existingPetID, existingPetName)
			return
		} else if err != sql.ErrNoRows {
			writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка проверки idempotency key")
			return
		}
	}

	eventID, err := database.InsertEvent(petID, req, idempotencyKey)
	if err != nil {
		if idempotencyKey != "" && database.IsUniqueViolation(err) {
			// Гонка параллельных запросов с одним и тем же idempotency key —
			// событие уже вставлено конкурентным запросом, возвращаем его.
			existing, existingPetID, existingPetName, lookupErr := database.GetEventByPetIDAndIdempotencyKey(petID, idempotencyKey)
			if lookupErr != nil {
				writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка при создании события")
				return
			}
			writeEventResponse(w, r, http.StatusCreated, existing, existingPetID, existingPetName)
			return
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка при создании события")
		return
	}

	createdEvent, petIDOfEvent, petName, err := database.GetEventByID(eventID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Событие создано, но не удалось получить его данные")
		return
	}

	writeEventResponse(w, r, http.StatusCreated, createdEvent, petIDOfEvent, petName)
}

// writeEventResponse строит EventResponse для одного события (POST /events,
// GET /events/{id}) и дополняет его полем `files` (см. «Файлы события —
// Backend», раздел «Чтение») перед отправкой ответа. Отдельной проверки
// владения для files не требуется — она уже выполнена при выборке самого
// события.
func writeEventResponse(w http.ResponseWriter, r *http.Request, status int, eventDB *models.EventDB, petID uuid.UUID, petName string) {
	response := eventResponseFromDB(eventDB, petID, petName)
	if !attachEventFiles(w, r, eventDB.ID, &response) {
		return
	}
	writeJSON(w, status, response)
}

// attachEventFiles заполняет поле Files ответа события подтверждёнными
// строками file (owner_type = event_file), в порядке position (см. «Файлы
// события — Backend», раздел «Чтение»). Files — всегда не-nil срез (может
// быть пустым), чтобы сериализоваться как `[]`, а не `null`.
func attachEventFiles(w http.ResponseWriter, r *http.Request, eventID uuid.UUID, response *models.EventResponse) bool {
	response.Files = []models.EventFileItem{}

	files, err := database.GetFilesForOwner(eventFileOwnerType, eventID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения файлов события")
		return false
	}
	if len(files) == 0 {
		return true
	}

	if storage == nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Хранилище файлов не сконфигурировано")
		return false
	}

	items := make([]models.EventFileItem, 0, len(files))
	for _, f := range files {
		url, err := storage.PresignGetURL(r.Context(), f.ObjectKey)
		if err != nil {
			writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось подписать ссылку на файл события")
			return false
		}
		var filename *string
		if f.Filename.Valid {
			filename = &f.Filename.String
		}
		items = append(items, models.EventFileItem{
			FileID:      f.ID.String(),
			URL:         url,
			ContentType: f.ContentType,
			Filename:    filename,
		})
	}
	response.Files = items
	return true
}

func eventResponseFromDB(eventDB *models.EventDB, petID uuid.UUID, petName string) models.EventResponse {
	var notes *string
	if eventDB.Notes.Valid {
		notes = &eventDB.Notes.String
	}
	return models.EventResponse{
		ID:      eventDB.ID.String(),
		Date:    eventDB.Date.Format(time.RFC3339),
		Type:    eventDB.Type,
		Value:   eventDB.Value,
		Notes:   notes,
		PetID:   petID.String(),
		PetName: petName,
	}
}

// PATCH /events/{id}
func UpdateEventHandler(w http.ResponseWriter, r *http.Request) {
	eventID, err := parseEventIDFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректный ID события")
		return
	}

	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	eventDB, err := database.GetEventByIDForUpdate(eventID)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Данное мероприятие не существует или не найдено")
			return
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения события")
		return
	}

	var req models.UpdateEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректное тело запроса")
		return
	}

	if req.PetID == "" {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Обязательное поле pet_id не заполнено")
		return
	}

	if req.Date == nil && req.Type == nil && req.Notes == nil && req.Value == nil {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Необходимо указать хотя бы одно поле для обновления")
		return
	}

	if req.Type != nil && req.Value == nil {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "При смене type поле value обязательно в этом же запросе")
		return
	}

	if !validateNotesLength(req.Notes) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поле notes не должно превышать 500 символов")
		return
	}

	reqPetID, err := uuid.Parse(req.PetID)
	if err != nil {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректный ID питомца")
		return
	}

	if eventDB.PetID != reqPetID {
		writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Данное мероприятие не существует или не найдено")
		return
	}

	belongs, err := database.CheckPetBelongsToUser(reqPetID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка проверки прав доступа")
		return
	}

	if !belongs {
		writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Питомец "+req.PetID+" не найден")
		return
	}

	petDB, err := database.GetPetById(reqPetID)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Питомец "+req.PetID+" не найден")
			return
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения питомца")
		return
	}

	if petDB.DeletedAt.Valid {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Невозможно редактировать запись о событии, так как питомец удален")
		return
	}

	if req.Type != nil && !eventreg.IsValidType(*req.Type) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректное значение type")
		return
	}

	if req.Value != nil {
		effectiveType := eventDB.Type
		if req.Type != nil {
			effectiveType = *req.Type
		}
		if msg := validateEventValue(effectiveType, *req.Value); msg != "" {
			writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, msg)
			return
		}
	}

	var dateTime *time.Time
	if req.Date != nil {
		parsedDate, err := parseEventDate(*req.Date)
		if err != nil {
			writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректный формат даты")
			return
		}
		dateTime = &parsedDate
	}

	if err := database.UpdateEvent(eventID, req, dateTime, req.Type, req.Notes, req.Value); err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка при обновлении события")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

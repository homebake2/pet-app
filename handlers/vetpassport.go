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
	"fmt"
	"myauthservice/database"
	"myauthservice/models"
	"myauthservice/openapi"
	"net/http"
	"regexp"
	"strings"
	"time"

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
// format=time), используемый event_time в GetVaccinationRequest/
// GetMedicationRequest.
func isValidTimeOfDay(s string) bool {
	return timeOfDayRe.MatchString(s)
}

// combineDateAndTime строит RFC3339 (UTC) момент времени из календарной даты
// и опционального времени суток (по умолчанию — полночь UTC) — используется
// при создании событий, связанных с прививкой/приёмом лекарства.
func combineDateAndTime(date time.Time, timeOfDay *string) string {
	hh, mm, ss := 0, 0, 0
	if timeOfDay != nil && *timeOfDay != "" {
		parts := strings.Split(*timeOfDay, ":")
		if len(parts) >= 2 {
			fmt.Sscanf(parts[0], "%d", &hh)
			fmt.Sscanf(parts[1], "%d", &mm)
		}
		if len(parts) == 3 {
			fmt.Sscanf(parts[2], "%d", &ss)
		}
	}
	return fmt.Sprintf("%sT%02d:%02d:%02dZ", date.Format("2006-01-02"), hh, mm, ss)
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

// createOtherEvent best-effort создаёт событие type=other со значением
// {"label": <name, обрезано до 50 рун>} на заданный момент времени —
// побочный эффект POST/PATCH /pet/{id}/vaccinations при
// add_event_on_administered/add_event_on_next=true (см. "Ведпаспорт —
// Backend"). Ошибка возвращается вызывающему (в отличие от
// createPetWeightEvent) — создание события здесь входит в основной
// контракт ответа (administered_event_id/next_event_id), а не является
// вспомогательной статистикой.
func createOtherEvent(petID uuid.UUID, date time.Time, timeOfDay *string, label string) (uuid.UUID, error) {
	value, err := json.Marshal(map[string]string{"label": truncateRunes(label, 50)})
	if err != nil {
		return uuid.Nil, err
	}
	req := models.CreateEventRequest{
		PetID: petID.String(),
		Date:  combineDateAndTime(date, timeOfDay),
		Type:  "other",
		Value: value,
	}
	return database.InsertEvent(petID, req, "")
}

// createMedicationEvent создаёт одно событие type=medication со значением
// {"name": <medication.name>} на заданную дату/время — используется
// POST /medications/{id}/events для (пере-)создания расписания приёма.
func createMedicationEvent(petID uuid.UUID, date time.Time, timeOfDay *string, name string) (uuid.UUID, error) {
	value, err := json.Marshal(map[string]string{"name": name})
	if err != nil {
		return uuid.Nil, err
	}
	req := models.CreateEventRequest{
		PetID: petID.String(),
		Date:  combineDateAndTime(date, timeOfDay),
		Type:  "medication",
		Value: value,
	}
	return database.InsertEvent(petID, req, "")
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
	if v.NextEventID.Valid {
		s := v.NextEventID.UUID.String()
		resp.NextEventID = &s
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
	if req.EventTime != nil && *req.EventTime != "" && !isValidTimeOfDay(*req.EventTime) {
		return "Некорректный формат event_time, ожидается HH:MM[:SS]"
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

	if !resolvePetForVetPassportCreate(w, petID, userID) {
		return
	}

	administeredDate, _ := parseDateOnly(req.AdministeredDate)

	var administeredEventID, nextEventID uuid.NullUUID
	if req.AddEventOnAdministered != nil && *req.AddEventOnAdministered {
		id, err := createOtherEvent(petID, administeredDate, req.EventTime, req.Name)
		if err != nil {
			writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось создать событие прививки")
			return
		}
		administeredEventID = uuid.NullUUID{UUID: id, Valid: true}
	}
	if req.AddEventOnNext != nil && *req.AddEventOnNext && req.NextDate != nil && *req.NextDate != "" {
		nextDate, _ := parseDateOnly(*req.NextDate)
		id, err := createOtherEvent(petID, nextDate, req.EventTime, req.Name)
		if err != nil {
			writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось создать событие-напоминание прививки")
			return
		}
		nextEventID = uuid.NullUUID{UUID: id, Valid: true}
	}

	newID, err := database.InsertVaccination(petID, req, administeredEventID, nextEventID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось создать прививку")
		return
	}

	writeJSON(w, http.StatusCreated, models.IDResponse{ID: newID.String()})
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

	var req models.UpdateVaccinationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректное тело запроса")
		return
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
	if req.EventTime != nil && *req.EventTime != "" && !isValidTimeOfDay(*req.EventTime) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректный формат event_time, ожидается HH:MM[:SS]")
		return
	}

	var administeredEventID, nextEventID *uuid.NullUUID
	effectiveName := vaccination.Name
	if req.Name != nil {
		effectiveName = *req.Name
	}
	if req.AddEventOnAdministered != nil && *req.AddEventOnAdministered {
		effectiveDate := vaccination.AdministeredDate
		if req.AdministeredDate != nil {
			effectiveDate, _ = parseDateOnly(*req.AdministeredDate)
		}
		newEventID, err := createOtherEvent(vaccination.PetID, effectiveDate, req.EventTime, effectiveName)
		if err != nil {
			writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось создать событие прививки")
			return
		}
		v := uuid.NullUUID{UUID: newEventID, Valid: true}
		administeredEventID = &v
	}
	if req.AddEventOnNext != nil && *req.AddEventOnNext {
		effectiveNextDate := vaccination.NextDate
		if req.NextDate != nil && *req.NextDate != "" {
			t, _ := parseDateOnly(*req.NextDate)
			effectiveNextDate = sql.NullTime{Time: t, Valid: true}
		}
		if effectiveNextDate.Valid {
			newEventID, err := createOtherEvent(vaccination.PetID, effectiveNextDate.Time, req.EventTime, effectiveName)
			if err != nil {
				writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось создать событие-напоминание прививки")
				return
			}
			v := uuid.NullUUID{UUID: newEventID, Valid: true}
			nextEventID = &v
		}
	}

	if err := database.UpdateVaccination(id, req, administeredEventID, nextEventID); err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка обновления прививки")
		return
	}

	w.WriteHeader(http.StatusNoContent)
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
	if err := database.SoftDeleteVaccination(id); err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Прививка не найдена")
			return
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка удаления прививки")
		return
	}
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
	newID, err := database.InsertDisease(petID, req)
	if err != nil {
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
	newID, err := database.InsertVetVisit(petID, req)
	if err != nil {
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
	newID, err := database.InsertAllergy(petID, req)
	if err != nil {
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

func medicationResponseFromDB(m models.MedicationDB, filesCount int) models.MedicationResponse {
	resp := models.MedicationResponse{
		ID:              m.ID.String(),
		PetID:           m.PetID.String(),
		Name:            m.Name,
		Dosage:          m.Dosage,
		PeriodicityDays: m.PeriodicityDays,
		StartDate:       m.StartDate.Format("2006-01-02"),
		RepeatCount:     m.RepeatCount,
		EventIDs:        m.EventIDs,
		FilesCount:      filesCount,
	}
	if resp.EventIDs == nil {
		resp.EventIDs = []string{}
	}
	if m.EventTime.Valid {
		resp.EventTime = &m.EventTime.String
	}
	if m.Note.Valid {
		resp.Note = &m.Note.String
	}
	return resp
}

func validateCreateMedicationRequest(req models.CreateMedicationRequest) string {
	if strings.TrimSpace(req.Name) == "" || len(req.Name) > models.MedicationNameMaxLen {
		return "Некорректное значение name"
	}
	if strings.TrimSpace(req.Dosage) == "" || len(req.Dosage) > models.MedicationDosageMaxLen {
		return "Некорректное значение dosage"
	}
	if req.PeriodicityDays < models.MedicationMinPeriodicityDays || req.PeriodicityDays > models.MedicationMaxPeriodicityDays {
		return "Поле periodicity_days должно быть в диапазоне 1-365"
	}
	if req.StartDate == "" || !isValidDateOnly(req.StartDate) {
		return "Некорректный формат start_date, ожидается YYYY-MM-DD"
	}
	if req.RepeatCount < models.MedicationMinRepeatCount || req.RepeatCount > models.MedicationMaxRepeatCount {
		return "Поле repeat_count должно быть в диапазоне 1-14"
	}
	if req.EventTime != nil && *req.EventTime != "" && !isValidTimeOfDay(*req.EventTime) {
		return "Некорректный формат event_time, ожидается HH:MM[:SS]"
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
	if _, ok := resolveOwnedPet(w, petID, userID); !ok {
		return
	}
	items, err := database.GetMedicationsByPetID(petID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения курсов лекарств")
		return
	}
	ids := make([]uuid.UUID, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	filesCounts, err := database.CountFilesForOwners(medicationFileOwnerType, ids)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения количества файлов")
		return
	}
	resp := models.MedicationListResponse{Items: make([]models.MedicationResponse, 0, len(items))}
	for _, it := range items {
		resp.Items = append(resp.Items, medicationResponseFromDB(it, filesCounts[it.ID]))
	}
	writeJSON(w, http.StatusOK, resp)
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
	if !resolvePetForVetPassportCreate(w, petID, userID) {
		return
	}
	newID, err := database.InsertMedication(petID, req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось создать курс лекарств")
		return
	}
	writeJSON(w, http.StatusCreated, models.IDResponse{ID: newID.String()})
}

// MedicationByIDHandler обрабатывает /medications/{id} (PATCH/DELETE) и
// /medications/{id}/events (POST/DELETE).
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

	if len(segments) == 2 && segments[1] == "events" {
		switch r.Method {
		case http.MethodPost:
			CreateMedicationEventsHandler(w, r, id)
		case http.MethodDelete:
			DeleteMedicationEventsHandler(w, r, id)
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
	if req.PeriodicityDays != nil && (*req.PeriodicityDays < models.MedicationMinPeriodicityDays || *req.PeriodicityDays > models.MedicationMaxPeriodicityDays) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поле periodicity_days должно быть в диапазоне 1-365")
		return
	}
	if req.StartDate != nil && !isValidDateOnly(*req.StartDate) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректный формат start_date, ожидается YYYY-MM-DD")
		return
	}
	if req.RepeatCount != nil && (*req.RepeatCount < models.MedicationMinRepeatCount || *req.RepeatCount > models.MedicationMaxRepeatCount) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поле repeat_count должно быть в диапазоне 1-14")
		return
	}
	if req.EventTime != nil && *req.EventTime != "" && !isValidTimeOfDay(*req.EventTime) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректный формат event_time, ожидается HH:MM[:SS]")
		return
	}
	if req.Note != nil && len(*req.Note) > models.MedicationNoteMaxLen {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поле note превышает допустимую длину")
		return
	}
	if err := database.UpdateMedication(id, req); err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка обновления курса лекарств")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func DeleteMedicationHandler(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
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
	if err := database.SoftDeleteMedication(id); err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Курс лекарств не найден")
			return
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка удаления курса лекарств")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// resolveOwnedMedicationForEvents находит курс лекарств по id и проверяет
// владение через его питомца — общий шаг POST/DELETE
// /medications/{id}/events.
func resolveOwnedMedicationForEvents(w http.ResponseWriter, id uuid.UUID, userID string) (*models.MedicationDB, bool) {
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

// CreateMedicationEventsHandler обрабатывает POST /medications/{id}/events:
// (пере-)создаёт связанные события приёма препарата по расписанию
// date[i] = start_date + i*periodicity_days, i от 0 до repeat_count-1.
// Предыдущий набор событий (если был) мягко удаляется перед вставкой нового
// — обычный soft-delete, в отличие от DELETE-варианта этого же эндпоинта.
func CreateMedicationEventsHandler(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	medication, ok := resolveOwnedMedicationForEvents(w, id, userID)
	if !ok {
		return
	}

	if err := database.SoftDeleteEventsByIDs(medication.EventIDs); err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось удалить предыдущие события курса")
		return
	}

	newEventIDs := make([]string, 0, medication.RepeatCount)
	for i := 0; i < medication.RepeatCount; i++ {
		date := medication.StartDate.AddDate(0, 0, i*medication.PeriodicityDays)
		var eventTime *string
		if medication.EventTime.Valid {
			eventTime = &medication.EventTime.String
		}
		eventID, err := createMedicationEvent(medication.PetID, date, eventTime, medication.Name)
		if err != nil {
			writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось создать событие приёма препарата")
			return
		}
		newEventIDs = append(newEventIDs, eventID.String())
	}

	if err := database.SetMedicationEventIDs(id, newEventIDs); err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось сохранить события курса")
		return
	}
	medication.EventIDs = newEventIDs

	filesCounts, err := database.CountFilesForOwners(medicationFileOwnerType, []uuid.UUID{medication.ID})
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения количества файлов")
		return
	}

	writeJSON(w, http.StatusOK, medicationResponseFromDB(*medication, filesCounts[medication.ID]))
}

// DeleteMedicationEventsHandler обрабатывает DELETE /medications/{id}/events —
// ЕДИНСТВЕННОЕ намеренное исключение из soft-delete во всём проекте: события
// удаляются физически (hard delete), см. описание операции
// delete-medication-events в open-api/spec.json.
func DeleteMedicationEventsHandler(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	medication, ok := resolveOwnedMedicationForEvents(w, id, userID)
	if !ok {
		return
	}

	if err := database.HardDeleteEventsByIDs(medication.EventIDs); err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось удалить события курса")
		return
	}
	if err := database.SetMedicationEventIDs(id, nil); err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось сохранить курс лекарств")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

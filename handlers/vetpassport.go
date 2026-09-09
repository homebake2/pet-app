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
	"sort"
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
// {"name": <medication.name>} на заданную дату/время и заметкой notes —
// используется расчётом расписания (см. handlers/medication_schedule.go) для
// (до-/пере-)создания событий приёма.
func createMedicationEvent(petID uuid.UUID, date time.Time, timeOfDay *string, name string, notes string) (uuid.UUID, error) {
	value, err := json.Marshal(map[string]string{"name": name})
	if err != nil {
		return uuid.Nil, err
	}
	req := models.CreateEventRequest{
		PetID: petID.String(),
		Date:  combineDateAndTime(date, timeOfDay),
		Type:  "medication",
		Notes: &notes,
		Value: value,
	}
	return database.InsertEvent(petID, req, "")
}

// createMedicationEventFromSlot материализует один слот расписания
// (см. computeMedicationScheduleSlots) в строку event: notes = slot.DoseNote,
// либо, если он не задан, dosageFallback (medication.dosage) — см. "Расчёт
// расписания", шаг 3.
func createMedicationEventFromSlot(petID uuid.UUID, slot medicationScheduleSlot, name, dosageFallback string) (uuid.UUID, error) {
	notes := dosageFallback
	if slot.DoseNote != nil && *slot.DoseNote != "" {
		notes = *slot.DoseNote
	}
	t := slot.Time
	return createMedicationEvent(petID, slot.Date, &t, name, notes)
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

// medicationResponseFromDB строит MedicationResponse, вычисляя next_dose по
// текущим полям расписания курса относительно now (см. "Вычисление
// next_dose").
func medicationResponseFromDB(m models.MedicationDB, filesCount int, now time.Time) models.MedicationResponse {
	resp := models.MedicationResponse{
		ID:            m.ID.String(),
		PetID:         m.PetID.String(),
		Name:          m.Name,
		Dosage:        m.Dosage,
		FrequencyType: m.FrequencyType,
		EventIDs:      m.EventIDs,
		FilesCount:    filesCount,
	}
	if resp.EventIDs == nil {
		resp.EventIDs = []string{}
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
		if nextDose := computeMedicationNextDose(m.FrequencyType, m.Weekdays, intervalDays, m.Times, startDate, endDate, now); nextDose != nil {
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
	if req.AddEvent != nil && *req.AddEvent && req.FrequencyType == models.MedicationFrequencyAsNeeded {
		return "Поле add_event недопустимо при frequency_type=as_needed"
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
		responses = append(responses, medicationResponseFromDB(it, filesCounts[it.ID], now))
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

	if req.AddEvent != nil && *req.AddEvent {
		startDate, _ := parseDateOnly(*req.StartDate)
		var endDate *time.Time
		if req.EndDate != nil {
			t, _ := parseDateOnly(*req.EndDate)
			endDate = &t
		}
		intervalDays := 0
		if req.IntervalDays != nil {
			intervalDays = *req.IntervalDays
		}
		slots := computeMedicationScheduleSlots(req.FrequencyType, req.Weekdays, intervalDays, req.Times, startDate, endDate, models.MedicationScheduleEventsCap)
		newEventIDs := make([]string, 0, len(slots))
		for _, slot := range slots {
			eventID, err := createMedicationEventFromSlot(petID, slot, req.Name, req.Dosage)
			if err != nil {
				writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось создать событие приёма препарата")
				return
			}
			newEventIDs = append(newEventIDs, eventID.String())
		}
		if err := database.SetMedicationEventIDs(newID, newEventIDs); err != nil {
			writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось сохранить события курса")
			return
		}
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
// frequency_type, и, если поля расписания изменились и у курса уже есть
// event_ids, либо оставляет события как есть (regenerate_events=false),
// либо пересоздаёт их (regenerate_events=true) — переход в as_needed с
// непустым event_ids без regenerate_events=true запрещён (400).
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

	// Эффективные (смёрженные с текущим состоянием курса) значения полей
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
	hasEvents := len(medication.EventIDs) > 0
	regenerate := req.RegenerateEvents != nil && *req.RegenerateEvents

	if scheduleFieldsChanged && hasEvents && !regenerate && effFreq == models.MedicationFrequencyAsNeeded {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Переход в frequency_type=as_needed с непустым event_ids требует regenerate_events=true")
		return
	}

	if err := database.UpdateMedication(id, req); err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка обновления курса лекарств")
		return
	}

	if scheduleFieldsChanged && hasEvents && regenerate {
		if err := database.HardDeleteEventsByIDs(medication.EventIDs); err != nil {
			writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось удалить предыдущие события курса")
			return
		}
		newEventIDs := []string{}
		if effFreq != models.MedicationFrequencyAsNeeded {
			startDate, _ := parseDateOnly(*effStartDate)
			var endDate *time.Time
			if effEndDate != nil {
				t, _ := parseDateOnly(*effEndDate)
				endDate = &t
			}
			intervalDays := 0
			if effIntervalDays != nil {
				intervalDays = *effIntervalDays
			}
			effName := medication.Name
			if req.Name != nil {
				effName = *req.Name
			}
			effDosage := medication.Dosage
			if req.Dosage != nil {
				effDosage = *req.Dosage
			}
			slots := computeMedicationScheduleSlots(effFreq, effWeekdays, intervalDays, effTimes, startDate, endDate, models.MedicationScheduleEventsCap)
			for _, slot := range slots {
				eventID, err := createMedicationEventFromSlot(medication.PetID, slot, effName, effDosage)
				if err != nil {
					writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось создать событие приёма препарата")
					return
				}
				newEventIDs = append(newEventIDs, eventID.String())
			}
		}
		if err := database.SetMedicationEventIDs(id, newEventIDs); err != nil {
			writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось сохранить события курса")
			return
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

// DeleteMedicationHandler обрабатывает DELETE /medications/{id}: мягко
// удаляет запись курса и, если у него есть event_ids, физически удаляет
// все связанные события (см. "Исключение из правила soft-delete").
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
	if len(medication.EventIDs) > 0 {
		if err := database.HardDeleteEventsByIDs(medication.EventIDs); err != nil {
			writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось удалить события курса")
			return
		}
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
// (до-)создаёт связанные события приёма препарата по расписанию,
// вычисленному из текущих frequency_type/weekdays/interval_days/times/
// start_date/end_date курса (не более 60 событий, см.
// computeMedicationScheduleSlots). Доступно только если event_ids пуст
// (иначе 409) и frequency_type != as_needed (иначе 400) — см. "Лекарства —
// Backend", раздел «Ручное создание набора событий».
func CreateMedicationEventsHandler(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	medication, ok := resolveOwnedMedicationForEvents(w, id, userID)
	if !ok {
		return
	}

	if len(medication.EventIDs) > 0 {
		writeError(w, http.StatusConflict, openapi.CONFLICT, "У курса лекарств уже есть набор событий — сначала удалите его")
		return
	}
	if medication.FrequencyType == models.MedicationFrequencyAsNeeded {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "У курса лекарств с frequency_type=as_needed нет расписания")
		return
	}

	intervalDays := 0
	if medication.IntervalDays.Valid {
		intervalDays = int(medication.IntervalDays.Int64)
	}
	var endDate *time.Time
	if medication.EndDate.Valid {
		endDate = &medication.EndDate.Time
	}
	slots := computeMedicationScheduleSlots(medication.FrequencyType, medication.Weekdays, intervalDays, medication.Times, medication.StartDate.Time, endDate, models.MedicationScheduleEventsCap)

	newEventIDs := make([]string, 0, len(slots))
	for _, slot := range slots {
		eventID, err := createMedicationEventFromSlot(medication.PetID, slot, medication.Name, medication.Dosage)
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

	writeJSON(w, http.StatusOK, medicationResponseFromDB(*medication, filesCounts[medication.ID], time.Now().UTC()))
}

// DeleteMedicationEventsHandler обрабатывает DELETE /medications/{id}/events —
// ЕДИНСТВЕННОЕ намеренное исключение из soft-delete во всём проекте: события
// удаляются физически (hard delete), см. описание операции
// delete-medication-events в open-api/spec.json. Требует непустого
// event_ids (иначе 404).
func DeleteMedicationEventsHandler(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	medication, ok := resolveOwnedMedicationForEvents(w, id, userID)
	if !ok {
		return
	}

	if len(medication.EventIDs) == 0 {
		writeError(w, http.StatusNotFound, openapi.NOTFOUND, "У курса лекарств нет набора событий")
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

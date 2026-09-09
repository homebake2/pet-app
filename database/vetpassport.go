// Ветпаспорт (медкарта питомца): SQL-слой для 5 pet_id-scoped сущностей —
// Vaccination, Disease, VetVisit, Allergy, Medication. Паттерн 1-в-1
// повторяет database/event.go: soft-delete через deleted_at, списки
// сортированы по "основной" дате по убыванию с пагинацией limit/offset,
// ownership-проверка для generic-механизма файлов сущностей (см.
// handlers/files.go) через Check<Entity>FileOwnership.
package database

import (
	"database/sql"
	"fmt"
	"log"
	"myauthservice/models"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// ---------------------------------------------------------------------------
// Vaccination
// ---------------------------------------------------------------------------

func InsertVaccination(petID uuid.UUID, req models.CreateVaccinationRequest, administeredEventID, nextEventID uuid.NullUUID) (uuid.UUID, error) {
	return insertVaccinationWith(DB, petID, req, administeredEventID, nextEventID)
}

// insertVaccinationWith — то же самое, что InsertVaccination, но принимает
// произвольный dbExecutor: используется как для обычных запросов (DB), так
// и внутри транзакции переноса локальных данных (см. ImportLocalData).
func insertVaccinationWith(exec dbExecutor, petID uuid.UUID, req models.CreateVaccinationRequest, administeredEventID, nextEventID uuid.NullUUID) (uuid.UUID, error) {
	administeredDate, err := time.Parse("2006-01-02", req.AdministeredDate)
	if err != nil {
		return uuid.Nil, err
	}

	var nextDate sql.NullTime
	if req.NextDate != nil {
		t, err := time.Parse("2006-01-02", *req.NextDate)
		if err != nil {
			return uuid.Nil, err
		}
		nextDate = sql.NullTime{Time: t, Valid: true}
	}

	var newID uuid.UUID
	err = exec.QueryRow(`
		INSERT INTO vaccination (pet_id, name, administered_date, next_date, administered_event_id, next_event_id)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id
	`, petID, req.Name, administeredDate, nextDate, administeredEventID, nextEventID).Scan(&newID)
	if err != nil {
		log.Println("InsertVaccination error:", err)
		return uuid.Nil, err
	}
	return newID, nil
}

func GetVaccinationsByPetID(petID uuid.UUID, limit, offset int) ([]models.VaccinationDB, error) {
	rows, err := DB.Query(`
		SELECT id, pet_id, name, administered_date, next_date, administered_event_id, next_event_id, deleted_at
		FROM vaccination
		WHERE pet_id = $1 AND deleted_at IS NULL
		ORDER BY administered_date DESC
		LIMIT $2 OFFSET $3
	`, petID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []models.VaccinationDB
	for rows.Next() {
		var v models.VaccinationDB
		if err := rows.Scan(&v.ID, &v.PetID, &v.Name, &v.AdministeredDate, &v.NextDate, &v.AdministeredEventID, &v.NextEventID, &v.DeletedAt); err != nil {
			return nil, err
		}
		items = append(items, v)
	}
	return items, rows.Err()
}

func GetVaccinationByIDForUpdate(id uuid.UUID) (*models.VaccinationDB, error) {
	var v models.VaccinationDB
	err := DB.QueryRow(`
		SELECT id, pet_id, name, administered_date, next_date, administered_event_id, next_event_id, deleted_at
		FROM vaccination
		WHERE id = $1 AND deleted_at IS NULL
	`, id).Scan(&v.ID, &v.PetID, &v.Name, &v.AdministeredDate, &v.NextDate, &v.AdministeredEventID, &v.NextEventID, &v.DeletedAt)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// UpdateVaccination обновляет только переданные поля. administeredEventID/
// nextEventID передаются non-nil только когда PATCH сам создал новое
// связанное событие (add_event_on_administered/add_event_on_next=true) —
// иначе существующие значения не трогаются.
func UpdateVaccination(id uuid.UUID, req models.UpdateVaccinationRequest, administeredEventID, nextEventID *uuid.NullUUID) error {
	setParts := []string{}
	args := []any{}
	argID := 1
	add := func(field string, value any) {
		setParts = append(setParts, field+" = $"+strconv.Itoa(argID))
		args = append(args, value)
		argID++
	}

	if req.Name != nil {
		add("name", *req.Name)
	}
	if req.AdministeredDate != nil {
		t, err := time.Parse("2006-01-02", *req.AdministeredDate)
		if err != nil {
			return err
		}
		add("administered_date", t)
	}
	if req.NextDate != nil {
		if *req.NextDate == "" {
			add("next_date", sql.NullTime{Valid: false})
		} else {
			t, err := time.Parse("2006-01-02", *req.NextDate)
			if err != nil {
				return err
			}
			add("next_date", sql.NullTime{Time: t, Valid: true})
		}
	}
	if administeredEventID != nil {
		add("administered_event_id", *administeredEventID)
	}
	if nextEventID != nil {
		add("next_event_id", *nextEventID)
	}

	if len(setParts) == 0 {
		return nil
	}

	query := fmt.Sprintf(`UPDATE vaccination SET %s WHERE id = $%d`, strings.Join(setParts, ", "), argID)
	args = append(args, id)
	_, err := DB.Exec(query, args...)
	return err
}

func SoftDeleteVaccination(id uuid.UUID) error {
	return softDeleteByID("vaccination", id)
}

// CheckVaccinationFileOwnership — правило владения для owner_type =
// "vaccination_file" (см. handlers/files.go): прививка не удалена и её
// питомец принадлежит (не удалённому) userID.
func CheckVaccinationFileOwnership(id uuid.UUID, userID string) (bool, error) {
	return checkChildFileOwnership("vaccination", id, userID)
}

// ---------------------------------------------------------------------------
// Disease
// ---------------------------------------------------------------------------

func InsertDisease(petID uuid.UUID, req models.CreateDiseaseRequest) (uuid.UUID, error) {
	return insertDiseaseWith(DB, petID, req)
}

func insertDiseaseWith(exec dbExecutor, petID uuid.UUID, req models.CreateDiseaseRequest) (uuid.UUID, error) {
	diagnosedDate, err := time.Parse("2006-01-02", req.DiagnosedDate)
	if err != nil {
		return uuid.Nil, err
	}
	var newID uuid.UUID
	err = exec.QueryRow(`
		INSERT INTO disease (pet_id, name, diagnosed_date, status, note)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`, petID, req.Name, diagnosedDate, req.Status, req.Note).Scan(&newID)
	if err != nil {
		log.Println("InsertDisease error:", err)
		return uuid.Nil, err
	}
	return newID, nil
}

func GetDiseasesByPetID(petID uuid.UUID, limit, offset int) ([]models.DiseaseDB, error) {
	rows, err := DB.Query(`
		SELECT id, pet_id, name, diagnosed_date, status, note, deleted_at
		FROM disease
		WHERE pet_id = $1 AND deleted_at IS NULL
		ORDER BY diagnosed_date DESC
		LIMIT $2 OFFSET $3
	`, petID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []models.DiseaseDB
	for rows.Next() {
		var d models.DiseaseDB
		if err := rows.Scan(&d.ID, &d.PetID, &d.Name, &d.DiagnosedDate, &d.Status, &d.Note, &d.DeletedAt); err != nil {
			return nil, err
		}
		items = append(items, d)
	}
	return items, rows.Err()
}

func GetDiseaseByIDForUpdate(id uuid.UUID) (*models.DiseaseDB, error) {
	var d models.DiseaseDB
	err := DB.QueryRow(`
		SELECT id, pet_id, name, diagnosed_date, status, note, deleted_at
		FROM disease
		WHERE id = $1 AND deleted_at IS NULL
	`, id).Scan(&d.ID, &d.PetID, &d.Name, &d.DiagnosedDate, &d.Status, &d.Note, &d.DeletedAt)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func UpdateDisease(id uuid.UUID, req models.UpdateDiseaseRequest) error {
	setParts := []string{}
	args := []any{}
	argID := 1
	add := func(field string, value any) {
		setParts = append(setParts, field+" = $"+strconv.Itoa(argID))
		args = append(args, value)
		argID++
	}

	if req.Name != nil {
		add("name", *req.Name)
	}
	if req.DiagnosedDate != nil {
		t, err := time.Parse("2006-01-02", *req.DiagnosedDate)
		if err != nil {
			return err
		}
		add("diagnosed_date", t)
	}
	if req.Status != nil {
		add("status", *req.Status)
	}
	if req.Note != nil {
		if *req.Note == "" {
			add("note", sql.NullString{Valid: false})
		} else {
			add("note", sql.NullString{String: *req.Note, Valid: true})
		}
	}

	if len(setParts) == 0 {
		return nil
	}

	query := fmt.Sprintf(`UPDATE disease SET %s WHERE id = $%d`, strings.Join(setParts, ", "), argID)
	args = append(args, id)
	_, err := DB.Exec(query, args...)
	return err
}

func SoftDeleteDisease(id uuid.UUID) error {
	return softDeleteByID("disease", id)
}

func CheckDiseaseFileOwnership(id uuid.UUID, userID string) (bool, error) {
	return checkChildFileOwnership("disease", id, userID)
}

// ---------------------------------------------------------------------------
// VetVisit
// ---------------------------------------------------------------------------

func InsertVetVisit(petID uuid.UUID, req models.CreateVetVisitRequest) (uuid.UUID, error) {
	return insertVetVisitWith(DB, petID, req)
}

func insertVetVisitWith(exec dbExecutor, petID uuid.UUID, req models.CreateVetVisitRequest) (uuid.UUID, error) {
	visitDate, err := time.Parse("2006-01-02", req.VisitDate)
	if err != nil {
		return uuid.Nil, err
	}
	var newID uuid.UUID
	err = exec.QueryRow(`
		INSERT INTO vet_visit (pet_id, visit_date, reason, clinic, note)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`, petID, visitDate, req.Reason, req.Clinic, req.Note).Scan(&newID)
	if err != nil {
		log.Println("InsertVetVisit error:", err)
		return uuid.Nil, err
	}
	return newID, nil
}

func GetVetVisitsByPetID(petID uuid.UUID, limit, offset int) ([]models.VetVisitDB, error) {
	rows, err := DB.Query(`
		SELECT id, pet_id, visit_date, reason, clinic, note, deleted_at
		FROM vet_visit
		WHERE pet_id = $1 AND deleted_at IS NULL
		ORDER BY visit_date DESC
		LIMIT $2 OFFSET $3
	`, petID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []models.VetVisitDB
	for rows.Next() {
		var v models.VetVisitDB
		if err := rows.Scan(&v.ID, &v.PetID, &v.VisitDate, &v.Reason, &v.Clinic, &v.Note, &v.DeletedAt); err != nil {
			return nil, err
		}
		items = append(items, v)
	}
	return items, rows.Err()
}

func GetVetVisitByIDForUpdate(id uuid.UUID) (*models.VetVisitDB, error) {
	var v models.VetVisitDB
	err := DB.QueryRow(`
		SELECT id, pet_id, visit_date, reason, clinic, note, deleted_at
		FROM vet_visit
		WHERE id = $1 AND deleted_at IS NULL
	`, id).Scan(&v.ID, &v.PetID, &v.VisitDate, &v.Reason, &v.Clinic, &v.Note, &v.DeletedAt)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func UpdateVetVisit(id uuid.UUID, req models.UpdateVetVisitRequest) error {
	setParts := []string{}
	args := []any{}
	argID := 1
	add := func(field string, value any) {
		setParts = append(setParts, field+" = $"+strconv.Itoa(argID))
		args = append(args, value)
		argID++
	}

	if req.VisitDate != nil {
		t, err := time.Parse("2006-01-02", *req.VisitDate)
		if err != nil {
			return err
		}
		add("visit_date", t)
	}
	if req.Reason != nil {
		add("reason", *req.Reason)
	}
	if req.Clinic != nil {
		if *req.Clinic == "" {
			add("clinic", sql.NullString{Valid: false})
		} else {
			add("clinic", sql.NullString{String: *req.Clinic, Valid: true})
		}
	}
	if req.Note != nil {
		if *req.Note == "" {
			add("note", sql.NullString{Valid: false})
		} else {
			add("note", sql.NullString{String: *req.Note, Valid: true})
		}
	}

	if len(setParts) == 0 {
		return nil
	}

	query := fmt.Sprintf(`UPDATE vet_visit SET %s WHERE id = $%d`, strings.Join(setParts, ", "), argID)
	args = append(args, id)
	_, err := DB.Exec(query, args...)
	return err
}

func SoftDeleteVetVisit(id uuid.UUID) error {
	return softDeleteByID("vet_visit", id)
}

func CheckVetVisitFileOwnership(id uuid.UUID, userID string) (bool, error) {
	return checkChildFileOwnership("vet_visit", id, userID)
}

// ---------------------------------------------------------------------------
// Allergy
// ---------------------------------------------------------------------------

func InsertAllergy(petID uuid.UUID, req models.CreateAllergyRequest) (uuid.UUID, error) {
	return insertAllergyWith(DB, petID, req)
}

func insertAllergyWith(exec dbExecutor, petID uuid.UUID, req models.CreateAllergyRequest) (uuid.UUID, error) {
	var detectedDate sql.NullTime
	if req.DetectedDate != nil {
		t, err := time.Parse("2006-01-02", *req.DetectedDate)
		if err != nil {
			return uuid.Nil, err
		}
		detectedDate = sql.NullTime{Time: t, Valid: true}
	}
	var newID uuid.UUID
	err := exec.QueryRow(`
		INSERT INTO allergy (pet_id, allergen, reaction, detected_date, severity, note)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id
	`, petID, req.Allergen, req.Reaction, detectedDate, req.Severity, req.Note).Scan(&newID)
	if err != nil {
		log.Println("InsertAllergy error:", err)
		return uuid.Nil, err
	}
	return newID, nil
}

func GetAllergiesByPetID(petID uuid.UUID, limit, offset int) ([]models.AllergyDB, error) {
	rows, err := DB.Query(`
		SELECT id, pet_id, allergen, reaction, detected_date, severity, note, deleted_at
		FROM allergy
		WHERE pet_id = $1 AND deleted_at IS NULL
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`, petID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []models.AllergyDB
	for rows.Next() {
		var a models.AllergyDB
		if err := rows.Scan(&a.ID, &a.PetID, &a.Allergen, &a.Reaction, &a.DetectedDate, &a.Severity, &a.Note, &a.DeletedAt); err != nil {
			return nil, err
		}
		items = append(items, a)
	}
	return items, rows.Err()
}

func GetAllergyByIDForUpdate(id uuid.UUID) (*models.AllergyDB, error) {
	var a models.AllergyDB
	err := DB.QueryRow(`
		SELECT id, pet_id, allergen, reaction, detected_date, severity, note, deleted_at
		FROM allergy
		WHERE id = $1 AND deleted_at IS NULL
	`, id).Scan(&a.ID, &a.PetID, &a.Allergen, &a.Reaction, &a.DetectedDate, &a.Severity, &a.Note, &a.DeletedAt)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func UpdateAllergy(id uuid.UUID, req models.UpdateAllergyRequest) error {
	setParts := []string{}
	args := []any{}
	argID := 1
	add := func(field string, value any) {
		setParts = append(setParts, field+" = $"+strconv.Itoa(argID))
		args = append(args, value)
		argID++
	}

	if req.Allergen != nil {
		add("allergen", *req.Allergen)
	}
	if req.Reaction != nil {
		if *req.Reaction == "" {
			add("reaction", sql.NullString{Valid: false})
		} else {
			add("reaction", sql.NullString{String: *req.Reaction, Valid: true})
		}
	}
	if req.DetectedDate != nil {
		if *req.DetectedDate == "" {
			add("detected_date", sql.NullTime{Valid: false})
		} else {
			t, err := time.Parse("2006-01-02", *req.DetectedDate)
			if err != nil {
				return err
			}
			add("detected_date", sql.NullTime{Time: t, Valid: true})
		}
	}
	if req.Severity != nil {
		add("severity", *req.Severity)
	}
	if req.Note != nil {
		if *req.Note == "" {
			add("note", sql.NullString{Valid: false})
		} else {
			add("note", sql.NullString{String: *req.Note, Valid: true})
		}
	}

	if len(setParts) == 0 {
		return nil
	}

	query := fmt.Sprintf(`UPDATE allergy SET %s WHERE id = $%d`, strings.Join(setParts, ", "), argID)
	args = append(args, id)
	_, err := DB.Exec(query, args...)
	return err
}

func SoftDeleteAllergy(id uuid.UUID) error {
	return softDeleteByID("allergy", id)
}

func CheckAllergyFileOwnership(id uuid.UUID, userID string) (bool, error) {
	return checkChildFileOwnership("allergy", id, userID)
}

// ---------------------------------------------------------------------------
// Medication
// ---------------------------------------------------------------------------

func InsertMedication(petID uuid.UUID, req models.CreateMedicationRequest) (uuid.UUID, error) {
	return insertMedicationWith(DB, petID, req)
}

func insertMedicationWith(exec dbExecutor, petID uuid.UUID, req models.CreateMedicationRequest) (uuid.UUID, error) {
	startDate, err := time.Parse("2006-01-02", req.StartDate)
	if err != nil {
		return uuid.Nil, err
	}
	var eventTime sql.NullString
	if req.EventTime != nil {
		eventTime = sql.NullString{String: *req.EventTime, Valid: true}
	}
	var newID uuid.UUID
	err = exec.QueryRow(`
		INSERT INTO medication (pet_id, name, dosage, periodicity_days, start_date, repeat_count, event_time, event_ids, note)
		VALUES ($1, $2, $3, $4, $5, $6, $7, '{}', $8)
		RETURNING id
	`, petID, req.Name, req.Dosage, req.PeriodicityDays, startDate, req.RepeatCount, eventTime, req.Note).Scan(&newID)
	if err != nil {
		log.Println("InsertMedication error:", err)
		return uuid.Nil, err
	}
	return newID, nil
}

func GetMedicationsByPetID(petID uuid.UUID, limit, offset int) ([]models.MedicationDB, error) {
	rows, err := DB.Query(`
		SELECT id, pet_id, name, dosage, periodicity_days, start_date, repeat_count, event_time, event_ids, note, deleted_at
		FROM medication
		WHERE pet_id = $1 AND deleted_at IS NULL
		ORDER BY start_date DESC
		LIMIT $2 OFFSET $3
	`, petID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []models.MedicationDB
	for rows.Next() {
		var m models.MedicationDB
		if err := rows.Scan(&m.ID, &m.PetID, &m.Name, &m.Dosage, &m.PeriodicityDays, &m.StartDate, &m.RepeatCount, &m.EventTime, pq.Array(&m.EventIDs), &m.Note, &m.DeletedAt); err != nil {
			return nil, err
		}
		items = append(items, m)
	}
	return items, rows.Err()
}

func GetMedicationByIDForUpdate(id uuid.UUID) (*models.MedicationDB, error) {
	var m models.MedicationDB
	err := DB.QueryRow(`
		SELECT id, pet_id, name, dosage, periodicity_days, start_date, repeat_count, event_time, event_ids, note, deleted_at
		FROM medication
		WHERE id = $1 AND deleted_at IS NULL
	`, id).Scan(&m.ID, &m.PetID, &m.Name, &m.Dosage, &m.PeriodicityDays, &m.StartDate, &m.RepeatCount, &m.EventTime, pq.Array(&m.EventIDs), &m.Note, &m.DeletedAt)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// UpdateMedication обновляет только переданные поля. event_ids этой функцией
// никогда не трогается — доступно только на чтение через
// POST/DELETE /medications/{id}/events (см. UpdateMedicationRequest в
// spec.json).
func UpdateMedication(id uuid.UUID, req models.UpdateMedicationRequest) error {
	setParts := []string{}
	args := []any{}
	argID := 1
	add := func(field string, value any) {
		setParts = append(setParts, field+" = $"+strconv.Itoa(argID))
		args = append(args, value)
		argID++
	}

	if req.Name != nil {
		add("name", *req.Name)
	}
	if req.Dosage != nil {
		add("dosage", *req.Dosage)
	}
	if req.PeriodicityDays != nil {
		add("periodicity_days", *req.PeriodicityDays)
	}
	if req.StartDate != nil {
		t, err := time.Parse("2006-01-02", *req.StartDate)
		if err != nil {
			return err
		}
		add("start_date", t)
	}
	if req.RepeatCount != nil {
		add("repeat_count", *req.RepeatCount)
	}
	if req.EventTime != nil {
		if *req.EventTime == "" {
			add("event_time", sql.NullString{Valid: false})
		} else {
			add("event_time", sql.NullString{String: *req.EventTime, Valid: true})
		}
	}
	if req.Note != nil {
		if *req.Note == "" {
			add("note", sql.NullString{Valid: false})
		} else {
			add("note", sql.NullString{String: *req.Note, Valid: true})
		}
	}

	if len(setParts) == 0 {
		return nil
	}

	query := fmt.Sprintf(`UPDATE medication SET %s WHERE id = $%d`, strings.Join(setParts, ", "), argID)
	args = append(args, id)
	_, err := DB.Exec(query, args...)
	return err
}

func SoftDeleteMedication(id uuid.UUID) error {
	return softDeleteByID("medication", id)
}

func CheckMedicationFileOwnership(id uuid.UUID, userID string) (bool, error) {
	return checkChildFileOwnership("medication", id, userID)
}

// SetMedicationEventIDs перезаписывает event_ids курса лекарств — вызывается
// после (пере-)создания событий приёма (POST /medications/{id}/events) или
// после их физического удаления (DELETE /medications/{id}/events, eventIDs=nil).
func SetMedicationEventIDs(id uuid.UUID, eventIDs []string) error {
	if eventIDs == nil {
		eventIDs = []string{}
	}
	_, err := DB.Exec(`UPDATE medication SET event_ids = $1 WHERE id = $2`, pq.Array(eventIDs), id)
	return err
}

// SoftDeleteEventsByIDs мягко удаляет события по списку id (используется
// при пересоздании расписания приёма препарата — POST /medications/{id}/events
// удаляет через soft-delete предыдущий набор событий перед вставкой нового).
func SoftDeleteEventsByIDs(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := DB.Exec(`UPDATE event SET deleted_at = now() WHERE id = ANY($1::uuid[]) AND deleted_at IS NULL`, pq.Array(ids))
	return err
}

// HardDeleteEventsByIDs физически удаляет события по списку id — единственное
// намеренное исключение из soft-delete во всём проекте, см.
// DELETE /medications/{id}/events в spec.json (описание операции
// delete-medication-events).
func HardDeleteEventsByIDs(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := DB.Exec(`DELETE FROM event WHERE id = ANY($1::uuid[])`, pq.Array(ids))
	return err
}

// ---------------------------------------------------------------------------
// Общие для всех 5 сущностей ветпаспорта хелперы.
// ---------------------------------------------------------------------------

// softDeleteByID — общая реализация мягкого удаления по id для таблиц
// ветпаспорта (vaccination/disease/vet_visit/allergy/medication): их схема
// одинакова в части id/deleted_at, поэтому таблица параметризована именем, а
// не дублированием пяти идентичных функций. table — константа, задаётся
// только вызовами внутри этого файла, поэтому конкатенация имени в SQL не
// является инъекцией пользовательских данных.
func softDeleteByID(table string, id uuid.UUID) error {
	query := fmt.Sprintf(`UPDATE %s SET deleted_at = $1 WHERE id = $2 AND deleted_at IS NULL`, table)
	now := time.Now().UTC()
	result, err := DB.Exec(query, now, id)
	if err != nil {
		log.Printf("softDeleteByID(%s) error: %v", table, err)
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// checkChildFileOwnership — общая реализация Check<Entity>FileOwnership для
// таблиц ветпаспорта: запись не удалена и её питомец принадлежит (не
// удалённому) userID — то же правило, что и CheckEventFileOwnership. table —
// константа, задаётся только вызовами внутри этого файла.
func checkChildFileOwnership(table string, id uuid.UUID, userID string) (bool, error) {
	query := fmt.Sprintf(`
		SELECT COUNT(1) FROM %s t
		WHERE t.id = $1 AND t.deleted_at IS NULL
		AND EXISTS (SELECT 1 FROM pet WHERE pet.id = t.pet_id AND pet.user_id = $2 AND pet.deleted_at IS NULL)
	`, table)
	var count int
	err := DB.QueryRow(query, id, userID).Scan(&count)
	if err != nil {
		return false, err
	}
	return count == 1, nil
}

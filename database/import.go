package database

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"myauthservice/models"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ReserveImportIdempotencyKey резервирует пару (user_id, idempotency_key)
// перед началом переноса локальных данных. reserved=true — ключ использован
// этим пользователем впервые, можно продолжать перенос как обычно.
// reserved=false — ключ уже зарегистрирован, см. GetImportResultByIdempotencyKey
// для определения дальнейшего шага. Аналогично ReservePetIdempotencyKey.
func ReserveImportIdempotencyKey(userID string, idempotencyKey string) (reserved bool, err error) {
	result, err := DB.Exec(`
		INSERT INTO import_local_data_idempotency_key (user_id, idempotency_key)
		VALUES ($1, $2)
		ON CONFLICT (user_id, idempotency_key) DO NOTHING
	`, userID, idempotencyKey)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows == 1, nil
}

// GetImportResultByIdempotencyKey возвращает ранее сохранённый результат
// переноса для (user_id, idempotency_key). hasResult=false означает редкую
// гонку параллельных запросов с одним ключом (резервирование ещё не
// завершено, см. FinalizeImportIdempotencyKey) — аналогично поведению
// GetPetIDByIdempotencyKey.
func GetImportResultByIdempotencyKey(userID string, idempotencyKey string) (result models.ImportLocalDataResponse, hasResult bool, err error) {
	var petsImported, eventsImported, reminderPlansImported sql.NullInt64
	var vaccinationsImported, diseasesImported, vetVisitsImported, allergiesImported, medicationsImported sql.NullInt64
	var profileImported sql.NullBool
	var petsMapping, eventsMapping sql.NullString
	var vaccinationsMapping, diseasesMapping, vetVisitsMapping, allergiesMapping, medicationsMapping, reminderPlansMapping sql.NullString

	err = DB.QueryRow(`
		SELECT pets_imported, events_imported, profile_imported, pets_mapping, events_mapping,
		       vaccinations_imported, diseases_imported, vet_visits_imported, allergies_imported, medications_imported,
		       vaccinations_mapping, diseases_mapping, vet_visits_mapping, allergies_mapping, medications_mapping,
		       reminder_plans_imported, reminder_plans_mapping
		FROM import_local_data_idempotency_key
		WHERE user_id = $1 AND idempotency_key = $2
	`, userID, idempotencyKey).Scan(
		&petsImported, &eventsImported, &profileImported, &petsMapping, &eventsMapping,
		&vaccinationsImported, &diseasesImported, &vetVisitsImported, &allergiesImported, &medicationsImported,
		&vaccinationsMapping, &diseasesMapping, &vetVisitsMapping, &allergiesMapping, &medicationsMapping,
		&reminderPlansImported, &reminderPlansMapping,
	)
	if err != nil {
		return models.ImportLocalDataResponse{}, false, err
	}

	if !petsImported.Valid {
		return models.ImportLocalDataResponse{}, false, nil
	}

	pets := []models.ImportedPet{}
	if petsMapping.Valid {
		if err = json.Unmarshal([]byte(petsMapping.String), &pets); err != nil {
			return models.ImportLocalDataResponse{}, false, err
		}
	}

	events := []models.ImportedEvent{}
	if eventsMapping.Valid {
		if err = json.Unmarshal([]byte(eventsMapping.String), &events); err != nil {
			return models.ImportLocalDataResponse{}, false, err
		}
	}

	vaccinations := []models.ImportedVaccination{}
	if vaccinationsMapping.Valid {
		if err = json.Unmarshal([]byte(vaccinationsMapping.String), &vaccinations); err != nil {
			return models.ImportLocalDataResponse{}, false, err
		}
	}
	diseases := []models.ImportedDisease{}
	if diseasesMapping.Valid {
		if err = json.Unmarshal([]byte(diseasesMapping.String), &diseases); err != nil {
			return models.ImportLocalDataResponse{}, false, err
		}
	}
	vetVisits := []models.ImportedVetVisit{}
	if vetVisitsMapping.Valid {
		if err = json.Unmarshal([]byte(vetVisitsMapping.String), &vetVisits); err != nil {
			return models.ImportLocalDataResponse{}, false, err
		}
	}
	allergies := []models.ImportedAllergy{}
	if allergiesMapping.Valid {
		if err = json.Unmarshal([]byte(allergiesMapping.String), &allergies); err != nil {
			return models.ImportLocalDataResponse{}, false, err
		}
	}
	medications := []models.ImportedMedication{}
	if medicationsMapping.Valid {
		if err = json.Unmarshal([]byte(medicationsMapping.String), &medications); err != nil {
			return models.ImportLocalDataResponse{}, false, err
		}
	}

	reminderPlans := []models.ImportedReminderPlan{}
	if reminderPlansMapping.Valid {
		if err = json.Unmarshal([]byte(reminderPlansMapping.String), &reminderPlans); err != nil {
			return models.ImportLocalDataResponse{}, false, err
		}
	}

	return models.ImportLocalDataResponse{
		PetsImported:         int(petsImported.Int64),
		EventsImported:       int(eventsImported.Int64),
		ProfileImported:      profileImported.Bool,
		Pets:                 pets,
		Events:               events,
		VaccinationsImported: int(vaccinationsImported.Int64),
		DiseasesImported:     int(diseasesImported.Int64),
		VetVisitsImported:    int(vetVisitsImported.Int64),
		AllergiesImported:    int(allergiesImported.Int64),
		MedicationsImported:  int(medicationsImported.Int64),
		Vaccinations:         vaccinations,
		Diseases:             diseases,
		VetVisits:            vetVisits,
		Allergies:            allergies,
		Medications:          medications,

		ReminderPlansImported: int(reminderPlansImported.Int64),
		ReminderPlans:         reminderPlans,
	}, true, nil
}

// FinalizeImportIdempotencyKey связывает ранее зарезервированный ключ с
// результатом успешно завершённого переноса.
func FinalizeImportIdempotencyKey(userID string, idempotencyKey string, result models.ImportLocalDataResponse) error {
	petsMapping, err := json.Marshal(result.Pets)
	if err != nil {
		return err
	}
	eventsMapping, err := json.Marshal(result.Events)
	if err != nil {
		return err
	}
	vaccinationsMapping, err := json.Marshal(result.Vaccinations)
	if err != nil {
		return err
	}
	diseasesMapping, err := json.Marshal(result.Diseases)
	if err != nil {
		return err
	}
	vetVisitsMapping, err := json.Marshal(result.VetVisits)
	if err != nil {
		return err
	}
	allergiesMapping, err := json.Marshal(result.Allergies)
	if err != nil {
		return err
	}
	medicationsMapping, err := json.Marshal(result.Medications)
	if err != nil {
		return err
	}
	reminderPlansMapping, err := json.Marshal(result.ReminderPlans)
	if err != nil {
		return err
	}
	_, err = DB.Exec(`
		UPDATE import_local_data_idempotency_key
		SET pets_imported = $1, events_imported = $2, profile_imported = $3, pets_mapping = $4, events_mapping = $5,
		    vaccinations_imported = $6, diseases_imported = $7, vet_visits_imported = $8, allergies_imported = $9, medications_imported = $10,
		    vaccinations_mapping = $11, diseases_mapping = $12, vet_visits_mapping = $13, allergies_mapping = $14, medications_mapping = $15,
		    reminder_plans_imported = $16, reminder_plans_mapping = $17
		WHERE user_id = $18 AND idempotency_key = $19
	`, result.PetsImported, result.EventsImported, result.ProfileImported, string(petsMapping), string(eventsMapping),
		result.VaccinationsImported, result.DiseasesImported, result.VetVisitsImported, result.AllergiesImported, result.MedicationsImported,
		string(vaccinationsMapping), string(diseasesMapping), string(vetVisitsMapping), string(allergiesMapping), string(medicationsMapping),
		result.ReminderPlansImported, string(reminderPlansMapping),
		userID, idempotencyKey)
	return err
}

// ImportLocalData атомарно переносит питомцев, события и (опционально)
// профиль из тела запроса POST /import/local-data в аккаунт userID: одна
// транзакция БД охватывает все вставки — либо создаются все перечисленные
// записи, либо (при любой ошибке) ни одна (см. "Импорт локальных данных —
// Backend", разделы 3 и 4). Сервер всегда выдаёт новые id; local_id/
// pet_local_id используются только для сопоставления питомец↔событие внутри
// этого вызова и нигде не сохраняются.
func ImportLocalData(userID string, req models.ImportLocalDataRequest) (result models.ImportLocalDataResponse, err error) {
	tx, err := DB.Begin()
	if err != nil {
		return result, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	localIDToServerID := make(map[string]uuid.UUID, len(req.Pets))
	for _, pet := range req.Pets {
		var petID uuid.UUID
		petID, err = insertPetWith(tx, userID, pet.ToCreatePetRequest())
		if err != nil {
			return result, err
		}
		localIDToServerID[pet.LocalID] = petID
	}

	eventLocalIDToServerID := make(map[string]uuid.UUID, len(req.Events))
	for _, event := range req.Events {
		petID, ok := localIDToServerID[event.PetLocalID]
		if !ok {
			// Не должно происходить: pet_local_id уже провалидирован
			// хендлером перед вызовом ImportLocalData. Защитная проверка на
			// случай рассинхрона между валидацией и этой функцией.
			err = fmt.Errorf("import: pet_local_id %q не найден среди перенесённых питомцев", event.PetLocalID)
			return result, err
		}
		var eventID uuid.UUID
		eventID, err = insertEventWith(tx, petID, event.ToCreateEventRequest(petID.String()), "")
		if err != nil {
			return result, err
		}
		eventLocalIDToServerID[event.LocalID] = eventID
	}

	// Настройки напоминаний переносятся ровно с теми напоминаниями, что
	// присланы: расписание на сервере не пересчитывается. Источник настроек
	// записывается ниже, когда вставлены лекарства и прививки, которые на них
	// ссылаются; до этого все настройки — manual.
	planLocalIDToServerID := make(map[string]uuid.UUID, len(req.ReminderPlans))
	importedReminderPlans := make([]models.ImportedReminderPlan, 0, len(req.ReminderPlans))
	for _, plan := range req.ReminderPlans {
		petID, ok := localIDToServerID[plan.PetLocalID]
		if !ok {
			err = fmt.Errorf("import: pet_local_id %q не найден среди перенесённых питомцев (reminder_plan)", plan.PetLocalID)
			return result, err
		}
		var planID uuid.UUID
		var reminderIDs []uuid.UUID
		planID, reminderIDs, err = insertImportedReminderPlan(tx, petID, plan)
		if err != nil {
			return result, err
		}
		planLocalIDToServerID[plan.LocalID] = planID
		imported := models.ImportedReminderPlan{LocalID: plan.LocalID, ID: planID.String(), Reminders: make([]models.ImportedReminder, 0, len(plan.Reminders))}
		for i, reminder := range plan.Reminders {
			imported.Reminders = append(imported.Reminders, models.ImportedReminder{LocalID: reminder.LocalID, ID: reminderIDs[i].String()})
		}
		importedReminderPlans = append(importedReminderPlans, imported)
	}

	profileImported := false
	if req.Profile != nil {
		if err = UpsertProfileWith(tx, userID, req.Profile.ToProfile(userID)); err != nil {
			return result, err
		}
		profileImported = true
	}

	// Ведпаспорт: vaccinations/diseases/vet_visits/allergies/medications —
	// та же схема, что и pets/events выше (local_id -> серверный id), плюс
	// vaccinations могут ссылаться на уже перенесённые события (см.
	// importVaccinationEventID) и medications — на event_local_ids для
	// связывания уже перенесённых local-событий приёма без пересчёта
	// расписания на сервере (см. models.ImportMedication).
	vaccinationLocalIDToServerID := make(map[string]uuid.UUID, len(req.Vaccinations))
	for _, v := range req.Vaccinations {
		petID, ok := localIDToServerID[v.PetLocalID]
		if !ok {
			err = fmt.Errorf("import: pet_local_id %q не найден среди перенесённых питомцев (vaccination)", v.PetLocalID)
			return result, err
		}
		administeredEventID, lookupErr := importVaccinationEventID(tx, petID, v.AdministeredEventLocalID, eventLocalIDToServerID, v.AddEventOnAdministered, v.AdministeredDate, v.EventTime, v.Name)
		if lookupErr != nil {
			err = lookupErr
			return result, err
		}
		var nextPlanID uuid.NullUUID
		if v.NextReminderPlanLocalID != nil && *v.NextReminderPlanLocalID != "" {
			serverID, found := planLocalIDToServerID[*v.NextReminderPlanLocalID]
			if !found {
				err = fmt.Errorf("import: next_reminder_plan_local_id %q не найден среди перенесённых настроек напоминаний (vaccination)", *v.NextReminderPlanLocalID)
				return result, err
			}
			nextPlanID = uuid.NullUUID{UUID: serverID, Valid: true}
		}
		var vaccinationID uuid.UUID
		vaccinationID, err = insertVaccinationWith(tx, petID, v.ToCreateVaccinationRequest(), administeredEventID, nextPlanID, "")
		if err != nil {
			return result, err
		}
		if nextPlanID.Valid {
			err = SetReminderPlanSourceWith(tx, nextPlanID.UUID, models.ReminderSourceVaccination, uuid.NullUUID{UUID: vaccinationID, Valid: true})
			if err != nil {
				return result, err
			}
		}
		vaccinationLocalIDToServerID[v.LocalID] = vaccinationID
	}

	diseaseLocalIDToServerID := make(map[string]uuid.UUID, len(req.Diseases))
	for _, d := range req.Diseases {
		petID, ok := localIDToServerID[d.PetLocalID]
		if !ok {
			err = fmt.Errorf("import: pet_local_id %q не найден среди перенесённых питомцев (disease)", d.PetLocalID)
			return result, err
		}
		var diseaseID uuid.UUID
		diseaseID, err = insertDiseaseWith(tx, petID, d.ToCreateDiseaseRequest(), "")
		if err != nil {
			return result, err
		}
		diseaseLocalIDToServerID[d.LocalID] = diseaseID
	}

	vetVisitLocalIDToServerID := make(map[string]uuid.UUID, len(req.VetVisits))
	for _, v := range req.VetVisits {
		petID, ok := localIDToServerID[v.PetLocalID]
		if !ok {
			err = fmt.Errorf("import: pet_local_id %q не найден среди перенесённых питомцев (vet_visit)", v.PetLocalID)
			return result, err
		}
		var vetVisitID uuid.UUID
		vetVisitID, err = insertVetVisitWith(tx, petID, v.ToCreateVetVisitRequest(), "")
		if err != nil {
			return result, err
		}
		vetVisitLocalIDToServerID[v.LocalID] = vetVisitID
	}

	allergyLocalIDToServerID := make(map[string]uuid.UUID, len(req.Allergies))
	for _, a := range req.Allergies {
		petID, ok := localIDToServerID[a.PetLocalID]
		if !ok {
			err = fmt.Errorf("import: pet_local_id %q не найден среди перенесённых питомцев (allergy)", a.PetLocalID)
			return result, err
		}
		var allergyID uuid.UUID
		allergyID, err = insertAllergyWith(tx, petID, a.ToCreateAllergyRequest(), "")
		if err != nil {
			return result, err
		}
		allergyLocalIDToServerID[a.LocalID] = allergyID
	}

	medicationLocalIDToServerID := make(map[string]uuid.UUID, len(req.Medications))
	for _, m := range req.Medications {
		petID, ok := localIDToServerID[m.PetLocalID]
		if !ok {
			err = fmt.Errorf("import: pet_local_id %q не найден среди перенесённых питомцев (medication)", m.PetLocalID)
			return result, err
		}
		var medicationID uuid.UUID
		medicationID, err = insertMedicationWith(tx, petID, m.ToCreateMedicationRequest(), "")
		if err != nil {
			return result, err
		}
		if m.ReminderPlanLocalID != nil && *m.ReminderPlanLocalID != "" {
			planID, found := planLocalIDToServerID[*m.ReminderPlanLocalID]
			if !found {
				err = fmt.Errorf("import: reminder_plan_local_id %q не найден среди перенесённых настроек напоминаний (medication)", *m.ReminderPlanLocalID)
				return result, err
			}
			if err = SetMedicationReminderPlanIDWith(tx, medicationID, uuid.NullUUID{UUID: planID, Valid: true}); err != nil {
				return result, err
			}
			err = SetReminderPlanSourceWith(tx, planID, models.ReminderSourceMedication, uuid.NullUUID{UUID: medicationID, Valid: true})
			if err != nil {
				return result, err
			}
		}
		medicationLocalIDToServerID[m.LocalID] = medicationID
	}

	if err = tx.Commit(); err != nil {
		return result, err
	}

	importedPets := make([]models.ImportedPet, 0, len(req.Pets))
	for _, pet := range req.Pets {
		importedPets = append(importedPets, models.ImportedPet{
			LocalID: pet.LocalID,
			ID:      localIDToServerID[pet.LocalID].String(),
		})
	}

	importedEvents := make([]models.ImportedEvent, 0, len(req.Events))
	for _, event := range req.Events {
		importedEvents = append(importedEvents, models.ImportedEvent{
			LocalID: event.LocalID,
			ID:      eventLocalIDToServerID[event.LocalID].String(),
		})
	}

	importedVaccinations := make([]models.ImportedVaccination, 0, len(req.Vaccinations))
	for _, v := range req.Vaccinations {
		importedVaccinations = append(importedVaccinations, models.ImportedVaccination{
			LocalID: v.LocalID,
			ID:      vaccinationLocalIDToServerID[v.LocalID].String(),
		})
	}
	importedDiseases := make([]models.ImportedDisease, 0, len(req.Diseases))
	for _, d := range req.Diseases {
		importedDiseases = append(importedDiseases, models.ImportedDisease{
			LocalID: d.LocalID,
			ID:      diseaseLocalIDToServerID[d.LocalID].String(),
		})
	}
	importedVetVisits := make([]models.ImportedVetVisit, 0, len(req.VetVisits))
	for _, v := range req.VetVisits {
		importedVetVisits = append(importedVetVisits, models.ImportedVetVisit{
			LocalID: v.LocalID,
			ID:      vetVisitLocalIDToServerID[v.LocalID].String(),
		})
	}
	importedAllergies := make([]models.ImportedAllergy, 0, len(req.Allergies))
	for _, a := range req.Allergies {
		importedAllergies = append(importedAllergies, models.ImportedAllergy{
			LocalID: a.LocalID,
			ID:      allergyLocalIDToServerID[a.LocalID].String(),
		})
	}
	importedMedications := make([]models.ImportedMedication, 0, len(req.Medications))
	for _, m := range req.Medications {
		importedMedications = append(importedMedications, models.ImportedMedication{
			LocalID: m.LocalID,
			ID:      medicationLocalIDToServerID[m.LocalID].String(),
		})
	}

	result = models.ImportLocalDataResponse{
		PetsImported:         len(req.Pets),
		EventsImported:       len(req.Events),
		ProfileImported:      profileImported,
		Pets:                 importedPets,
		Events:               importedEvents,
		VaccinationsImported: len(req.Vaccinations),
		DiseasesImported:     len(req.Diseases),
		VetVisitsImported:    len(req.VetVisits),
		AllergiesImported:    len(req.Allergies),
		MedicationsImported:  len(req.Medications),
		Vaccinations:         importedVaccinations,
		Diseases:             importedDiseases,
		VetVisits:            importedVetVisits,
		Allergies:            importedAllergies,
		Medications:          importedMedications,

		ReminderPlansImported: len(req.ReminderPlans),
		ReminderPlans:         importedReminderPlans,
	}
	return result, nil
}

// derefOrEmpty возвращает *s, либо "" если s == nil — используется для
// передачи опциональных дат в importVaccinationEventID без дополнительного
// ветвления в вызывающем коде.
func derefOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// importVaccinationEventID определяет administered_event_id импортируемой
// прививки: если клиент передал administered_event_local_id — факт уже был
// перенесён в этом же запросе (events[]), достаточно найти его серверный id;
// иначе, если addEvent=true и date непусто — создаёт новый факт (type=other)
// тем же способом, что и POST /pet/{id}/vaccinations
// (см. handlers/vetpassport.go: createOtherEvent). Дублирование построения
// value/date здесь небольшое и оправдано отсутствием доступа пакета
// database к пакету handlers (см. слоение: handlers -> database, не
// наоборот).
func importVaccinationEventID(exec dbExecutor, petID uuid.UUID, eventLocalID *string, eventLocalIDToServerID map[string]uuid.UUID, addEvent *bool, date string, eventTime *string, label string) (uuid.NullUUID, error) {
	if eventLocalID != nil && *eventLocalID != "" {
		serverID, ok := eventLocalIDToServerID[*eventLocalID]
		if !ok {
			return uuid.NullUUID{}, fmt.Errorf("import: event_local_id %q не найден среди перенесённых событий (vaccination)", *eventLocalID)
		}
		return uuid.NullUUID{UUID: serverID, Valid: true}, nil
	}
	if addEvent == nil || !*addEvent || date == "" {
		return uuid.NullUUID{}, nil
	}
	parsedDate, err := time.Parse("2006-01-02", date)
	if err != nil {
		return uuid.NullUUID{}, err
	}
	hh, mm, ss := 0, 0, 0
	if eventTime != nil && *eventTime != "" {
		parts := strings.Split(*eventTime, ":")
		if len(parts) >= 2 {
			fmt.Sscanf(parts[0], "%d", &hh)
			fmt.Sscanf(parts[1], "%d", &mm)
		}
		if len(parts) == 3 {
			fmt.Sscanf(parts[2], "%d", &ss)
		}
	}
	dateTimeStr := fmt.Sprintf("%sT%02d:%02d:%02dZ", parsedDate.Format("2006-01-02"), hh, mm, ss)
	value, err := json.Marshal(map[string]string{"label": truncateLabel(label, 50)})
	if err != nil {
		return uuid.NullUUID{}, err
	}
	req := models.CreateEventRequest{
		PetID: petID.String(),
		Date:  dateTimeStr,
		Type:  "other",
		Value: value,
	}
	eventID, err := insertEventWith(exec, petID, req, "")
	if err != nil {
		return uuid.NullUUID{}, err
	}
	return uuid.NullUUID{UUID: eventID, Valid: true}, nil
}

// truncateLabel обрезает строку до maxLen рун — см.
// handlers/vetpassport.go:truncateRunes (та же логика, продублирована здесь
// по причине слоения пакетов, см. importVaccinationEventID).
func truncateLabel(s string, maxLen int) string {
	r := []rune(s)
	if len(r) <= maxLen {
		return s
	}
	return string(r[:maxLen])
}

// insertImportedReminderPlan вставляет перенесённые настройки напоминания
// (источник manual — реальный источник записывается после вставки лекарств и
// прививок) и по одной строке reminder на каждое присланное напоминание.
// Расписание не пересчитывается. Возвращает id настроек и id напоминаний в
// порядке plan.Reminders.
func insertImportedReminderPlan(exec dbExecutor, petID uuid.UUID, plan models.ImportReminderPlan) (uuid.UUID, []uuid.UUID, error) {
	startDate, err := time.Parse("2006-01-02", plan.StartDate)
	if err != nil {
		return uuid.Nil, nil, err
	}
	var endDate sql.NullTime
	if plan.EndDate != nil && *plan.EndDate != "" {
		t, err := time.Parse("2006-01-02", *plan.EndDate)
		if err != nil {
			return uuid.Nil, nil, err
		}
		endDate = sql.NullTime{Time: t, Valid: true}
	}
	var intervalDays sql.NullInt64
	if plan.IntervalDays != nil {
		intervalDays = sql.NullInt64{Int64: int64(*plan.IntervalDays), Valid: true}
	}
	var notes sql.NullString
	if plan.Notes != nil && *plan.Notes != "" {
		notes = sql.NullString{String: *plan.Notes, Valid: true}
	}
	planID := uuid.New()
	planRow := models.ReminderPlanDB{
		ID:            planID,
		PetID:         petID,
		Source:        models.ReminderSourceManual,
		Type:          plan.Type,
		Value:         plan.Value,
		Notes:         notes,
		FrequencyType: plan.FrequencyType,
		Weekdays:      plan.Weekdays,
		IntervalDays:  intervalDays,
		Times:         plan.Times,
		StartDate:     startDate,
		EndDate:       endDate,
		TZ:            plan.TZ,
	}
	moments := make([]models.ReminderMoment, 0, len(plan.Reminders))
	for _, reminder := range plan.Reminders {
		remindAt, err := time.Parse(time.RFC3339, reminder.RemindAt)
		if err != nil {
			return uuid.Nil, nil, err
		}
		moments = append(moments, models.ReminderMoment{RemindAt: remindAt, Notes: reminder.Notes})
	}
	// Вставка строки настроек без напоминаний, затем напоминаний — чтобы
	// получить их id в порядке запроса.
	if err := InsertReminderPlanWith(exec, planRow, nil); err != nil {
		return uuid.Nil, nil, err
	}
	reminderIDs, err := InsertRemindersWith(exec, planID, moments)
	if err != nil {
		return uuid.Nil, nil, err
	}
	return planID, reminderIDs, nil
}

package models

import "encoding/json"

// ImportLocalDataPet — элемент pets[] в теле запроса POST /import/local-data.
// Набор полей питомца совпадает с CreatePetRequest, дополнен local_id —
// клиентским временным ключом ссылки, используемым только внутри этого
// запроса (не сохраняется на сервере).
type ImportLocalDataPet struct {
	LocalID    string  `json:"local_id"`
	Name       string  `json:"name"`
	Gender     *string `json:"gender,omitempty"`
	Species    string  `json:"species"`
	BirthDate  *string `json:"birth_date,omitempty"`
	Color      *string `json:"color,omitempty"`
	Sterilized *bool   `json:"sterilized,omitempty"`
	Habitation *string `json:"habitation,omitempty"`
	Notes      *string `json:"notes,omitempty"`
	Breed      *string `json:"breed,omitempty"`
	Icon       *string `json:"icon,omitempty"`
	// BodyCondition — кондиция тела питомца, опционально (см. «Ведпаспорт —
	// Backend», паритет сущностей при переносе).
	BodyCondition *string `json:"body_condition,omitempty"`
}

// ToCreatePetRequest конвертирует элемент pets[] в тот же тип запроса, что
// принимает POST /pet, чтобы переиспользовать вставку без дублирования кода.
func (p ImportLocalDataPet) ToCreatePetRequest() CreatePetRequest {
	return CreatePetRequest{
		Name:          p.Name,
		Gender:        p.Gender,
		Species:       p.Species,
		BirthDate:     p.BirthDate,
		Color:         p.Color,
		Sterilized:    p.Sterilized,
		Habitation:    p.Habitation,
		Notes:         p.Notes,
		Breed:         p.Breed,
		Icon:          p.Icon,
		BodyCondition: p.BodyCondition,
	}
}

// ImportLocalDataEvent — элемент events[] в теле запроса POST /import/local-data.
// Набор полей события совпадает с CreateEventRequest; вместо pet_id
// используется ссылка pet_local_id на pets[].local_id этого же запроса.
// LocalID — клиентский временный ключ (уникальный в пределах events[]),
// используется только для сопоставления в ответе (поле events), не
// сохраняется на сервере.
type ImportLocalDataEvent struct {
	LocalID    string          `json:"local_id"`
	PetLocalID string          `json:"pet_local_id"`
	Date       string          `json:"date"`
	Type       string          `json:"type"`
	Notes      *string         `json:"notes,omitempty"`
	Value      json.RawMessage `json:"value"`
}

// ToCreateEventRequest конвертирует элемент events[] в тот же тип запроса,
// что принимает POST /events, подставляя уже разрешённый серверный id питомца.
func (e ImportLocalDataEvent) ToCreateEventRequest(petID string) CreateEventRequest {
	return CreateEventRequest{
		PetID: petID,
		Date:  e.Date,
		Type:  e.Type,
		Notes: e.Notes,
		Value: e.Value,
	}
}

// ImportLocalDataProfile — поле profile в теле запроса POST /import/local-data.
// Набор полей совпадает с телом POST /profile.
type ImportLocalDataProfile struct {
	FirstName  string  `json:"first_name"`
	MiddleName *string `json:"middle_name,omitempty"`
	LastName   *string `json:"last_name,omitempty"`
	Email      *string `json:"email,omitempty"`
	Phone      *string `json:"phone,omitempty"`
}

// ToProfile конвертирует profile в тот же тип, что принимает POST /profile.
func (p ImportLocalDataProfile) ToProfile(userID string) Profile {
	return Profile{
		UserID:     userID,
		FirstName:  p.FirstName,
		MiddleName: p.MiddleName,
		LastName:   p.LastName,
		Email:      p.Email,
		Phone:      p.Phone,
	}
}

// ImportLocalDataRequest — тело запроса POST /import/local-data. Pets/Events
// умышленно без `omitempty`/указателя: отсутствие ключа и JSON null
// декодируются в nil-срез одинаково, и оба случая должны быть отклонены
// валидацией хендлера (поля обязательны, хоть и могут быть пустым массивом).
// Vaccinations/Diseases/VetVisits/Allergies/Medications — опциональные
// массивы ветпаспорта (см. "Ведпаспорт — Backend"); отсутствие/null
// трактуются как "нечего переносить", в отличие от pets/events.
type ImportLocalDataRequest struct {
	Profile      *ImportLocalDataProfile `json:"profile"`
	Pets         []ImportLocalDataPet    `json:"pets"`
	Events       []ImportLocalDataEvent  `json:"events"`
	Vaccinations []ImportVaccination     `json:"vaccinations,omitempty"`
	Diseases     []ImportDisease         `json:"diseases,omitempty"`
	VetVisits    []ImportVetVisit        `json:"vet_visits,omitempty"`
	Allergies    []ImportAllergy         `json:"allergies,omitempty"`
	Medications  []ImportMedication      `json:"medications,omitempty"`
}

// ImportVaccination — элемент vaccinations[] в теле запроса POST /import/local-data.
type ImportVaccination struct {
	LocalID                  string  `json:"local_id"`
	PetLocalID               string  `json:"pet_local_id"`
	Name                     string  `json:"name"`
	AdministeredDate         string  `json:"administered_date"`
	NextDate                 *string `json:"next_date,omitempty"`
	AddEventOnAdministered   *bool   `json:"add_event_on_administered,omitempty"`
	AddEventOnNext           *bool   `json:"add_event_on_next,omitempty"`
	EventTime                *string `json:"event_time,omitempty"`
	AdministeredEventLocalID *string `json:"administered_event_local_id,omitempty"`
	NextEventLocalID         *string `json:"next_event_local_id,omitempty"`
}

// ToCreateVaccinationRequest конвертирует элемент vaccinations[] в тот же
// тип запроса, что принимает POST /pet/{id}/vaccinations.
func (v ImportVaccination) ToCreateVaccinationRequest() CreateVaccinationRequest {
	return CreateVaccinationRequest{
		Name:                   v.Name,
		AdministeredDate:       v.AdministeredDate,
		NextDate:               v.NextDate,
		AddEventOnAdministered: v.AddEventOnAdministered,
		AddEventOnNext:         v.AddEventOnNext,
		EventTime:              v.EventTime,
	}
}

// ImportDisease — элемент diseases[] в теле запроса POST /import/local-data.
type ImportDisease struct {
	LocalID       string  `json:"local_id"`
	PetLocalID    string  `json:"pet_local_id"`
	Name          string  `json:"name"`
	DiagnosedDate string  `json:"diagnosed_date"`
	Status        string  `json:"status"`
	Note          *string `json:"note,omitempty"`
}

func (d ImportDisease) ToCreateDiseaseRequest() CreateDiseaseRequest {
	return CreateDiseaseRequest{
		Name:          d.Name,
		DiagnosedDate: d.DiagnosedDate,
		Status:        d.Status,
		Note:          d.Note,
	}
}

// ImportVetVisit — элемент vet_visits[] в теле запроса POST /import/local-data.
type ImportVetVisit struct {
	LocalID    string  `json:"local_id"`
	PetLocalID string  `json:"pet_local_id"`
	VisitDate  string  `json:"visit_date"`
	Reason     string  `json:"reason"`
	Clinic     *string `json:"clinic,omitempty"`
	Note       *string `json:"note,omitempty"`
}

func (v ImportVetVisit) ToCreateVetVisitRequest() CreateVetVisitRequest {
	return CreateVetVisitRequest{
		VisitDate: v.VisitDate,
		Reason:    v.Reason,
		Clinic:    v.Clinic,
		Note:      v.Note,
	}
}

// ImportAllergy — элемент allergies[] в теле запроса POST /import/local-data.
type ImportAllergy struct {
	LocalID      string  `json:"local_id"`
	PetLocalID   string  `json:"pet_local_id"`
	Allergen     string  `json:"allergen"`
	Reaction     *string `json:"reaction,omitempty"`
	DetectedDate *string `json:"detected_date,omitempty"`
	Severity     string  `json:"severity"`
	Note         *string `json:"note,omitempty"`
}

func (a ImportAllergy) ToCreateAllergyRequest() CreateAllergyRequest {
	return CreateAllergyRequest{
		Allergen:     a.Allergen,
		Reaction:     a.Reaction,
		DetectedDate: a.DetectedDate,
		Severity:     a.Severity,
		Note:         a.Note,
	}
}

// ImportMedication — элемент medications[] в теле запроса POST /import/local-data.
// EventLocalIDs здесь намеренно не используется сервером для генерации
// расписания приёмов при переносе (сервер не пересчитывает event_ids на
// импорте — см. "Ведпаспорт — Backend"/"Импорт локальных данных — Backend",
// шаг 9: те события, что уже перечислены в event_local_ids и найдены среди
// events[] этого же запроса, переносятся как есть в event_ids курса; сервер
// не вызывает расчёт расписания).
type ImportMedication struct {
	LocalID       string               `json:"local_id"`
	PetLocalID    string               `json:"pet_local_id"`
	Name          string               `json:"name"`
	Dosage        string               `json:"dosage"`
	FrequencyType string               `json:"frequency_type"`
	Weekdays      []int                `json:"weekdays,omitempty"`
	IntervalDays  *int                 `json:"interval_days,omitempty"`
	Times         []MedicationTimeSlot `json:"times,omitempty"`
	StartDate     *string              `json:"start_date,omitempty"`
	EndDate       *string              `json:"end_date,omitempty"`
	Note          *string              `json:"note,omitempty"`
	EventLocalIDs []string             `json:"event_local_ids,omitempty"`
}

func (m ImportMedication) ToCreateMedicationRequest() CreateMedicationRequest {
	return CreateMedicationRequest{
		Name:          m.Name,
		Dosage:        m.Dosage,
		FrequencyType: m.FrequencyType,
		Weekdays:      m.Weekdays,
		IntervalDays:  m.IntervalDays,
		Times:         m.Times,
		StartDate:     m.StartDate,
		EndDate:       m.EndDate,
		Note:          m.Note,
	}
}

// ImportedVaccination/ImportedDisease/ImportedVetVisit/ImportedAllergy/ImportedMedication
// — сопоставления local_id -> серверный id для соответствующих сущностей
// ветпаспорта в ответе ImportLocalDataResponse, симметрично ImportedPet.
type ImportedVaccination struct {
	LocalID string `json:"local_id"`
	ID      string `json:"id"`
}

type ImportedDisease struct {
	LocalID string `json:"local_id"`
	ID      string `json:"id"`
}

type ImportedVetVisit struct {
	LocalID string `json:"local_id"`
	ID      string `json:"id"`
}

type ImportedAllergy struct {
	LocalID string `json:"local_id"`
	ID      string `json:"id"`
}

type ImportedMedication struct {
	LocalID string `json:"local_id"`
	ID      string `json:"id"`
}

// ImportedPet — элемент поля pets ответа ImportLocalDataResponse: сопоставление
// клиентского local_id перенесённого питомца с его новым серверным id (см.
// "Импорт локальных данных — Backend", раздел 3). Используется клиентом для
// последующего фонового переноса фотографий (см. "Фотография питомца —
// Frontend (dataSource=local)").
type ImportedPet struct {
	LocalID string `json:"local_id"`
	ID      string `json:"id"`
}

// ImportedEvent — элемент поля events ответа ImportLocalDataResponse:
// сопоставление клиентского local_id перенесённого события с его новым
// серверным id (см. "Импорт локальных данных — Backend", раздел 3),
// симметрично ImportedPet. Используется клиентом для последующего фонового
// переноса файлов события (см. "Файлы события — Frontend (dataSource=local)").
type ImportedEvent struct {
	LocalID string `json:"local_id"`
	ID      string `json:"id"`
}

// ImportLocalDataResponse — тело ответа 200 OK POST /import/local-data.
type ImportLocalDataResponse struct {
	PetsImported         int                   `json:"pets_imported"`
	EventsImported       int                   `json:"events_imported"`
	ProfileImported      bool                  `json:"profile_imported"`
	Pets                 []ImportedPet         `json:"pets"`
	Events               []ImportedEvent       `json:"events"`
	VaccinationsImported int                   `json:"vaccinations_imported"`
	DiseasesImported     int                   `json:"diseases_imported"`
	VetVisitsImported    int                   `json:"vet_visits_imported"`
	AllergiesImported    int                   `json:"allergies_imported"`
	MedicationsImported  int                   `json:"medications_imported"`
	Vaccinations         []ImportedVaccination `json:"vaccinations"`
	Diseases             []ImportedDisease     `json:"diseases"`
	VetVisits            []ImportedVetVisit    `json:"vet_visits"`
	Allergies            []ImportedAllergy     `json:"allergies"`
	Medications          []ImportedMedication  `json:"medications"`
}

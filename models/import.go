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
}

// ToCreatePetRequest конвертирует элемент pets[] в тот же тип запроса, что
// принимает POST /pet, чтобы переиспользовать вставку без дублирования кода.
func (p ImportLocalDataPet) ToCreatePetRequest() CreatePetRequest {
	return CreatePetRequest{
		Name:       p.Name,
		Gender:     p.Gender,
		Species:    p.Species,
		BirthDate:  p.BirthDate,
		Color:      p.Color,
		Sterilized: p.Sterilized,
		Habitation: p.Habitation,
		Notes:      p.Notes,
		Breed:      p.Breed,
		Icon:       p.Icon,
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
type ImportLocalDataRequest struct {
	Profile *ImportLocalDataProfile `json:"profile"`
	Pets    []ImportLocalDataPet    `json:"pets"`
	Events  []ImportLocalDataEvent  `json:"events"`
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
	PetsImported    int             `json:"pets_imported"`
	EventsImported  int             `json:"events_imported"`
	ProfileImported bool            `json:"profile_imported"`
	Pets            []ImportedPet   `json:"pets"`
	Events          []ImportedEvent `json:"events"`
}

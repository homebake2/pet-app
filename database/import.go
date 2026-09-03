package database

import (
	"database/sql"
	"fmt"
	"myauthservice/models"

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
	var petsImported, eventsImported sql.NullInt64
	var profileImported sql.NullBool

	err = DB.QueryRow(`
		SELECT pets_imported, events_imported, profile_imported
		FROM import_local_data_idempotency_key
		WHERE user_id = $1 AND idempotency_key = $2
	`, userID, idempotencyKey).Scan(&petsImported, &eventsImported, &profileImported)
	if err != nil {
		return models.ImportLocalDataResponse{}, false, err
	}

	if !petsImported.Valid {
		return models.ImportLocalDataResponse{}, false, nil
	}

	return models.ImportLocalDataResponse{
		PetsImported:    int(petsImported.Int64),
		EventsImported:  int(eventsImported.Int64),
		ProfileImported: profileImported.Bool,
	}, true, nil
}

// FinalizeImportIdempotencyKey связывает ранее зарезервированный ключ с
// результатом успешно завершённого переноса.
func FinalizeImportIdempotencyKey(userID string, idempotencyKey string, result models.ImportLocalDataResponse) error {
	_, err := DB.Exec(`
		UPDATE import_local_data_idempotency_key
		SET pets_imported = $1, events_imported = $2, profile_imported = $3
		WHERE user_id = $4 AND idempotency_key = $5
	`, result.PetsImported, result.EventsImported, result.ProfileImported, userID, idempotencyKey)
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

	for _, event := range req.Events {
		petID, ok := localIDToServerID[event.PetLocalID]
		if !ok {
			// Не должно происходить: pet_local_id уже провалидирован
			// хендлером перед вызовом ImportLocalData. Защитная проверка на
			// случай рассинхрона между валидацией и этой функцией.
			err = fmt.Errorf("import: pet_local_id %q не найден среди перенесённых питомцев", event.PetLocalID)
			return result, err
		}
		if _, err = insertEventWith(tx, petID, event.ToCreateEventRequest(petID.String()), ""); err != nil {
			return result, err
		}
	}

	profileImported := false
	if req.Profile != nil {
		if err = UpsertProfileWith(tx, userID, req.Profile.ToProfile(userID)); err != nil {
			return result, err
		}
		profileImported = true
	}

	if err = tx.Commit(); err != nil {
		return result, err
	}

	result = models.ImportLocalDataResponse{
		PetsImported:    len(req.Pets),
		EventsImported:  len(req.Events),
		ProfileImported: profileImported,
	}
	return result, nil
}

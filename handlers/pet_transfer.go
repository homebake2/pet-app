package handlers

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"math/big"
	"myauthservice/database"
	"myauthservice/openapi"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Передача питомца другому пользователю (см. database/pet_transfer.go):
// владелец создаёт передачу и получает одноразовый код, получатель по коду
// просматривает питомца и принимает либо отклоняет передачу.

const (
	petTransferCodeLength = 10
	// petTransferCodeAlphabet — алфавит кода без визуально неоднозначных
	// знаков: нет 0/O и 1/I/L.
	petTransferCodeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
	// petTransferCodeAttempts — сколько раз повторяется создание передачи при
	// совпадении сгенерированного кода с кодом другой активной передачи.
	petTransferCodeAttempts = 5
)

// generatePetTransferCode возвращает случайный код передачи из
// криптостойкого генератора.
func generatePetTransferCode() (string, error) {
	alphabetSize := big.NewInt(int64(len(petTransferCodeAlphabet)))
	code := make([]byte, petTransferCodeLength)
	for i := range code {
		index, err := rand.Int(rand.Reader, alphabetSize)
		if err != nil {
			return "", err
		}
		code[i] = petTransferCodeAlphabet[index.Int64()]
	}
	return string(code), nil
}

// normalizePetTransferCode приводит введённый код к каноническому виду:
// регистр, пробелы и дефисы игнорируются. ok=false — после нормализации
// значение не соответствует формату кода.
func normalizePetTransferCode(input string) (code string, ok bool) {
	var builder strings.Builder
	for _, r := range strings.ToUpper(input) {
		if r == ' ' || r == '-' || r == '\t' || r == '\n' || r == '\r' {
			continue
		}
		builder.WriteRune(r)
	}
	code = builder.String()
	if len(code) != petTransferCodeLength {
		return "", false
	}
	for i := 0; i < len(code); i++ {
		if !strings.ContainsRune(petTransferCodeAlphabet, rune(code[i])) {
			return "", false
		}
	}
	return code, true
}

// PetTransferHandler обрабатывает /pet/{id}/transfer (создание, активная
// передача, отмена).
func PetTransferHandler(w http.ResponseWriter, r *http.Request, petID uuid.UUID) {
	switch r.Method {
	case http.MethodPost:
		createPetTransferHandler(w, r, petID)
	case http.MethodGet:
		getPetTransferHandler(w, r, petID)
	case http.MethodDelete:
		cancelPetTransferHandler(w, r, petID)
	default:
		writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
	}
}

func writePetTransfer(w http.ResponseWriter, status int, transfer *database.PetTransfer) {
	writeJSON(w, status, openapi.PetTransferResponse{
		Id:        openapi_types.UUID(transfer.ID),
		Code:      transfer.Code,
		ExpiresAt: transfer.ExpiresAt,
	})
}

// createPetTransferHandler обрабатывает POST /pet/{id}/transfer.
func createPetTransferHandler(w http.ResponseWriter, r *http.Request, petID uuid.UUID) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	// Передавать питомца может только владелец: участник с любой ролью — 403,
	// пользователь без доступа — 404.
	if _, ok := resolvePetRole(w, petID, userID, petNeedOwner); !ok {
		return
	}

	for attempt := 0; attempt < petTransferCodeAttempts; attempt++ {
		code, err := generatePetTransferCode()
		if err != nil {
			writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось создать код передачи")
			return
		}
		transfer, err := database.CreatePetTransfer(petID, userID, code)
		if err == nil {
			writePetTransfer(w, http.StatusCreated, transfer)
			return
		}
		if errors.Is(err, database.ErrTransferCodeCollision) {
			continue
		}
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Питомец не найден")
			return
		}
		if errors.Is(err, database.ErrTransferAlreadyPending) {
			writeError(w, http.StatusConflict, openapi.TRANSFERALREADYPENDING, "У питомца уже есть активная передача")
			return
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось создать передачу")
		return
	}
	writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось создать уникальный код передачи")
}

// getPetTransferHandler обрабатывает GET /pet/{id}/transfer.
func getPetTransferHandler(w http.ResponseWriter, r *http.Request, petID uuid.UUID) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	if _, ok := resolvePetRole(w, petID, userID, petNeedOwner); !ok {
		return
	}

	transfer, err := database.GetActivePetTransfer(petID, userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Активная передача не найдена")
			return
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения передачи")
		return
	}
	writePetTransfer(w, http.StatusOK, transfer)
}

// cancelPetTransferHandler обрабатывает DELETE /pet/{id}/transfer.
func cancelPetTransferHandler(w http.ResponseWriter, r *http.Request, petID uuid.UUID) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	if _, ok := resolvePetRole(w, petID, userID, petNeedOwner); !ok {
		return
	}

	if err := database.CancelActivePetTransfer(petID, userID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Активная передача не найдена")
			return
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка отмены передачи")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// readCodeRequest выполняет общие шаги эндпоинтов с кодом — передачи питомца и
// приглашения к доступу, у которых один формат кода и общий счётчик неудачных
// обращений: метод, авторизация, разбор и нормализация кода, ограничение
// подбора. При отказе сама пишет ответ и возвращает ok=false.
func readCodeRequest(w http.ResponseWriter, r *http.Request) (userID string, code string, ok bool) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
		return "", "", false
	}

	userID, ok = requireUserID(w, r)
	if !ok {
		return "", "", false
	}

	var req openapi.PetTransferCodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректное тело запроса")
		return "", "", false
	}
	code, valid := normalizePetTransferCode(req.Code)
	if !valid {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректный код")
		return "", "", false
	}

	failures, err := database.CountPetTransferCodeFailures(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка проверки лимита")
		return "", "", false
	}
	if failures >= database.PetTransferCodeFailureLimit {
		writeError(w, http.StatusTooManyRequests, openapi.RATELIMITED, "Превышен лимит неудачных попыток ввода кода, попробуйте позже")
		return "", "", false
	}
	return userID, code, true
}

// writeInvalidCode учитывает неудачное обращение с кодом и пишет единый 404 с
// сообщением message — причины недействительности кода не различаются.
func writeInvalidCode(w http.ResponseWriter, userID string, message string) {
	if err := database.RecordPetTransferCodeFailure(userID); err != nil {
		log.Printf("RecordPetTransferCodeFailure error: %v", err)
	}
	writeError(w, http.StatusNotFound, openapi.NOTFOUND, message)
}

// handlePetTransferCodeError пишет ответ по ошибке операции с кодом передачи.
func handlePetTransferCodeError(w http.ResponseWriter, userID string, err error, internalMessage string) {
	if errors.Is(err, sql.ErrNoRows) {
		writeInvalidCode(w, userID, "Код передачи недействителен")
		return
	}
	if errors.Is(err, database.ErrTransferOwnPet) {
		writeError(w, http.StatusConflict, openapi.TRANSFEROWNPET, "Нельзя принять или отклонить собственную передачу")
		return
	}
	writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, internalMessage)
}

// PetTransferPreviewHandler обрабатывает POST /pet-transfers/preview.
func PetTransferPreviewHandler(w http.ResponseWriter, r *http.Request) {
	userID, code, ok := readCodeRequest(w, r)
	if !ok {
		return
	}

	transfer, err := database.FindPendingPetTransferByCode(code)
	if err != nil {
		handlePetTransferCodeError(w, userID, err, "Ошибка поиска передачи")
		return
	}
	if transfer.FromUserID == userID {
		handlePetTransferCodeError(w, userID, database.ErrTransferOwnPet, "")
		return
	}

	pet, err := database.GetPetForUser(transfer.PetID, transfer.FromUserID)
	if err != nil {
		handlePetTransferCodeError(w, userID, err, "Ошибка получения питомца")
		return
	}

	senderName, err := database.GetPetTransferSenderName(transfer.FromUserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения отправителя")
		return
	}

	photoURL, ok := petTransferPhotoURL(w, r, transfer.PetID)
	if !ok {
		return
	}

	preview := openapi.PetTransferPreviewResponse{
		ExpiresAt:  transfer.ExpiresAt,
		SenderName: senderName,
		Pet: openapi.PetTransferPreviewPet{
			Name:     pet.Name,
			Species:  pet.Species,
			PhotoUrl: photoURL,
		},
	}
	if pet.Breed != nil {
		preview.Pet.Breed = *pet.Breed
	}
	if pet.Gender != nil {
		gender := openapi.GetGenderEnum(*pet.Gender)
		preview.Pet.Gender = &gender
	}
	if pet.BirthDate != nil {
		birthDate, err := time.Parse("2006-01-02", *pet.BirthDate)
		if err == nil {
			preview.Pet.BirthDate = &openapi_types.Date{Time: birthDate}
		}
	}
	writeJSON(w, http.StatusOK, preview)
}

// petTransferPhotoURL возвращает presigned GET URL фотографии питомца либо
// nil, если фотографии нет. При ошибке сама пишет ответ и возвращает ok=false.
func petTransferPhotoURL(w http.ResponseWriter, r *http.Request, petID uuid.UUID) (url *string, ok bool) {
	file, err := database.GetConfirmedFileForOwner(petPhotoOwnerType, petID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, true
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения фотографии питомца")
		return nil, false
	}
	if storage == nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Хранилище файлов не сконфигурировано")
		return nil, false
	}
	signed, err := storage.PresignGetURL(r.Context(), file.ObjectKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось подписать ссылку на фотографию")
		return nil, false
	}
	return &signed, true
}

// PetTransferDeclineHandler обрабатывает POST /pet-transfers/decline.
func PetTransferDeclineHandler(w http.ResponseWriter, r *http.Request) {
	userID, code, ok := readCodeRequest(w, r)
	if !ok {
		return
	}

	if err := database.DeclinePetTransfer(code, userID); err != nil {
		handlePetTransferCodeError(w, userID, err, "Ошибка отклонения передачи")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// PetTransferAcceptHandler обрабатывает POST /pet-transfers/accept.
func PetTransferAcceptHandler(w http.ResponseWriter, r *http.Request) {
	userID, code, ok := readCodeRequest(w, r)
	if !ok {
		return
	}

	petID, orphanKeys, err := database.AcceptPetTransfer(code, userID)
	if err != nil {
		handlePetTransferCodeError(w, userID, err, "Ошибка принятия передачи")
		return
	}
	deleteOrphanedObjects(r.Context(), orphanKeys)

	writeJSON(w, http.StatusOK, openapi.PetTransferAcceptResponse{PetId: openapi_types.UUID(petID)})
}

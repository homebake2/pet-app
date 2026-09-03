package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"myauthservice/database"
	"myauthservice/models"
	"myauthservice/openapi"
	"net/http"
	"net/mail"
	"regexp"
	"strconv"
	"strings"
)

// phoneRegex — тот же паттерн, что используется на фронте для валидации телефона.
// Компилируется один раз при старте пакета, а не на каждый запрос.
var phoneRegex = regexp.MustCompile(`^\+?[0-9\s\-().]{1,20}$`)

const (
	maxProfileNameLength  = 100
	maxProfileEmailLength = 254
	maxProfilePhoneLength = 20
)

// validateProfileNameLength проверяет длину first_name/middle_name/last_name.
func validateProfileNameLength(fieldName, value string) string {
	if len(value) > maxProfileNameLength {
		return fmt.Sprintf("Поле %s не должно превышать %d символов", fieldName, maxProfileNameLength)
	}
	return ""
}

// validateProfileEmail проверяет формат (net/mail) и длину email.
// Пустая строка трактуется как явная очистка поля и не проверяется на формат.
func validateProfileEmail(value string) string {
	if value == "" {
		return ""
	}
	if len(value) > maxProfileEmailLength {
		return fmt.Sprintf("Поле email не должно превышать %d символов", maxProfileEmailLength)
	}
	if _, err := mail.ParseAddress(value); err != nil {
		return "Некорректный формат email"
	}
	return ""
}

// validateProfilePhone проверяет формат (regexp) и длину телефона.
// Пустая строка трактуется как явная очистка поля и не проверяется на формат.
func validateProfilePhone(value string) string {
	if value == "" {
		return ""
	}
	if len(value) > maxProfilePhoneLength {
		return fmt.Sprintf("Поле phone не должно превышать %d символов", maxProfilePhoneLength)
	}
	if !phoneRegex.MatchString(value) {
		return "Некорректный формат телефона"
	}
	return ""
}

func ProfileHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		GetProfileHandler(w, r)
	case http.MethodPut:
		UpdateProfileHandler(w, r)
	case http.MethodPost:
		CreateProfileHandler(w, r)
	case http.MethodDelete:
		DeleteAccountHandler(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
	}
}

// DeleteAccountHandler обрабатывает DELETE /profile — удаляет аккаунт
// пользователя целиком. profile/pet/event удаляются каскадно через
// ON DELETE CASCADE (см. database/migrations/000001_init_schema.up.sql).
func DeleteAccountHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	if err := database.DeleteUser(userID); err != nil {
		log.Printf("DB delete user error: %v", err)
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось удалить аккаунт")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// CreateProfileHandler обрабатывает POST /profile
func CreateProfileHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var input models.Profile
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректное тело запроса")
		return
	}

	if strings.TrimSpace(input.FirstName) == "" {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поле first_name обязательно")
		return
	}

	if msg := validateProfileNameLength("first_name", input.FirstName); msg != "" {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, msg)
		return
	}
	if input.MiddleName != nil {
		if msg := validateProfileNameLength("middle_name", *input.MiddleName); msg != "" {
			writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, msg)
			return
		}
	}
	if input.LastName != nil {
		if msg := validateProfileNameLength("last_name", *input.LastName); msg != "" {
			writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, msg)
			return
		}
	}
	if input.Email != nil {
		if msg := validateProfileEmail(*input.Email); msg != "" {
			writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, msg)
			return
		}
	}
	if input.Phone != nil {
		if msg := validateProfilePhone(*input.Phone); msg != "" {
			writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, msg)
			return
		}
	}

	input.UserID = userID
	if err := database.UpsertProfileWith(database.DB, userID, input); err != nil {
		log.Printf("DB upsert error: %v", err)
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось сохранить профиль")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func UpdateProfileHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var input struct {
		FirstName  *string `json:"first_name"`
		MiddleName *string `json:"middle_name"`
		LastName   *string `json:"last_name"`
		Email      *string `json:"email"`
		Phone      *string `json:"phone"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректное тело запроса")
		return
	}

	if input.FirstName != nil && strings.TrimSpace(*input.FirstName) == "" {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поле first_name не может быть пустым")
		return
	}

	if input.FirstName != nil {
		if msg := validateProfileNameLength("first_name", *input.FirstName); msg != "" {
			writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, msg)
			return
		}
	}
	if input.MiddleName != nil {
		if msg := validateProfileNameLength("middle_name", *input.MiddleName); msg != "" {
			writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, msg)
			return
		}
	}
	if input.LastName != nil {
		if msg := validateProfileNameLength("last_name", *input.LastName); msg != "" {
			writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, msg)
			return
		}
	}
	if input.Email != nil {
		if msg := validateProfileEmail(*input.Email); msg != "" {
			writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, msg)
			return
		}
	}
	if input.Phone != nil {
		if msg := validateProfilePhone(*input.Phone); msg != "" {
			writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, msg)
			return
		}
	}

	setClauses := []string{}
	args := []interface{}{}
	argIdx := 1

	if input.FirstName != nil {
		setClauses = append(setClauses, "first_name=$"+strconv.Itoa(argIdx))
		args = append(args, *input.FirstName)
		argIdx++
	}
	if input.MiddleName != nil {
		setClauses = append(setClauses, "middle_name=$"+strconv.Itoa(argIdx))
		args = append(args, *input.MiddleName)
		argIdx++
	}
	if input.LastName != nil {
		setClauses = append(setClauses, "last_name=$"+strconv.Itoa(argIdx))
		args = append(args, *input.LastName)
		argIdx++
	}
	if input.Email != nil {
		setClauses = append(setClauses, "email=$"+strconv.Itoa(argIdx))
		args = append(args, *input.Email)
		argIdx++
	}
	if input.Phone != nil {
		setClauses = append(setClauses, "phone=$"+strconv.Itoa(argIdx))
		args = append(args, *input.Phone)
		argIdx++
	}

	if len(setClauses) == 0 {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Не передано ни одного поля для обновления")
		return
	}

	query := "UPDATE profile SET " + strings.Join(setClauses, ", ") + " WHERE user_id=$" + strconv.Itoa(argIdx)
	args = append(args, userID)

	result, err := database.DB.Exec(query, args...)
	if err != nil {
		log.Printf("DB update error: %v", err)
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось обновить профиль")
		return
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		log.Printf("DB rows affected error: %v", err)
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось обновить профиль")
		return
	}
	if rowsAffected == 0 {
		writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Профиль не найден, используйте POST для создания")
		return
	}

	w.WriteHeader(http.StatusOK)
}

func GetProfileHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	profile, err := database.GetProfileByUserID(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка базы данных")
		return
	}
	if profile == nil {
		writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Профиль не найден")
		return
	}

	login, err := database.GetLoginByUserID(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось получить логин")
		return
	}
	if login == "" {
		// profile.user_id ссылается на users(id) внешним ключом, поэтому
		// пустой login означает ошибку выполнения запроса (например,
		// некорректный JOIN), а не отсутствие пользователя.
		log.Printf("Empty login for existing profile, user_id=%s", userID)
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось получить логин")
		return
	}

	resp := models.ProfileResponse{
		ID:         profile.UserID,
		FirstName:  profile.FirstName,
		MiddleName: profile.MiddleName,
		LastName:   profile.LastName,
		Email:      profile.Email,
		Phone:      profile.Phone,
		Login:      login,
	}

	writeJSON(w, http.StatusOK, resp)
}

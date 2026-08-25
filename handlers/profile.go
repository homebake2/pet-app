package handlers

import (
	"encoding/json"
	"log"
	"myauthservice/database"
	"myauthservice/models"
	"myauthservice/openapi"
	"net/http"
	"strconv"
	"strings"
)

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

	const queryFind = "SELECT COUNT(*) FROM profile WHERE user_id=$1"
	var count int
	if err := database.DB.QueryRow(queryFind, userID).Scan(&count); err != nil {
		log.Printf("DB error: %v", err)
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка базы данных")
		return
	}

	if count == 0 {
		const queryInsert = `
			INSERT INTO profile (user_id, first_name, middle_name, last_name, email, phone)
			VALUES ($1, $2, $3, $4, $5, $6)
		`
		if _, err := database.DB.Exec(queryInsert, userID, input.FirstName, input.MiddleName, input.LastName, input.Email, input.Phone); err != nil {
			log.Printf("DB insert error: %v", err)
			writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось создать профиль")
			return
		}
	} else {
		const queryUpdate = `
			UPDATE profile
			SET first_name=$1,
				middle_name=$2,
				last_name=$3,
				email=$4,
				phone=$5
			WHERE user_id=$6
		`
		if _, err := database.DB.Exec(queryUpdate, input.FirstName, input.MiddleName, input.LastName, input.Email, input.Phone, userID); err != nil {
			log.Printf("DB update error: %v", err)
			writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось обновить профиль")
			return
		}
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

	if input.FirstName == nil || strings.TrimSpace(*input.FirstName) == "" {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поле first_name обязательно")
		return
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

	if _, err := database.DB.Exec(query, args...); err != nil {
		log.Printf("DB update error: %v", err)
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось обновить профиль")
		return
	}

	w.WriteHeader(http.StatusOK)
}

func GetProfileHandler(w http.ResponseWriter, r *http.Request) {
	ptrToString := func(s *string) string {
		if s != nil {
			return *s
		}
		return ""
	}

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

	resp := models.ProfileResponse{
		ID:         profile.UserID,
		FirstName:  profile.FirstName,
		MiddleName: ptrToString(profile.MiddleName),
		LastName:   ptrToString(profile.LastName),
		Email:      ptrToString(profile.Email),
		Phone:      ptrToString(profile.Phone),
		Login:      login,
	}

	writeJSON(w, http.StatusOK, resp)
}

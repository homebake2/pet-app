package handlers

import (
	"encoding/json"
	"log"
	"myauthservice/database"
	"myauthservice/models"
	"myauthservice/utils"
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
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Method not allowed",
			Code:  "MethodNotAllowed",
		})
	}
}

// CreateProfileHandler обрабатывает POST /profile
func CreateProfileHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Method not allowed",
			Code:  "MethodNotAllowed",
		})
		return
	}

	// 1. Парсим токен из заголовка Authorization
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Authorization header missing",
			Code:  "AuthorizationHeaderMissing",
		})
		return
	}

	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || parts[0] != "Bearer" {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Invalid authorization header",
			Code:  "InvalidAuthorizationHeader",
		})
		return
	}

	token := parts[1]

	// 2. Проверяем валидность токена и получаем claims
	claims, err := utils.ParseToken(token)
	if err != nil {
		// В utils можно сделать: возвращать ошибку или nil
		// Свои ошибки можно расширить
		if strings.Contains(err.Error(), "token is expired") {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(models.ErrorResponse{
				Error: "Token expired",
				Code:  "TokenExpired",
			})
			return
		} else {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(models.ErrorResponse{
				Error: "Invalid token",
				Code:  "InvalidToken",
			})
			return
		}
	}

	// 3. Из claims получаем user_id
	userID, ok := claims["id"].(string)
	if !ok || userID == "" {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Invalid token payload",
			Code:  "InvalidTokenPayload",
		})
		return
	}

	// 4. Находим пользователя в базе (опционально, если есть необходимость)
	// В данном случае предполагаем, что user_id есть и валиден.

	// 5. Обрабатываем тело запроса
	var input models.Profile
	err = json.NewDecoder(r.Body).Decode(&input)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Invalid request body",
			Code:  "InvalidRequestBody",
		})
		return
	}

	// Обязательное поле
	if strings.TrimSpace(input.FirstName) == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "first_name is required",
			Code:  "FirstNameRequired",
		})
		return
	}

	// 6. Проверяем, есть ли профиль у пользователя, и создаем или обновляем
	const queryFind = "SELECT COUNT(*) FROM profile WHERE user_id=$1"
	var count int
	err = database.DB.QueryRow(queryFind, userID).Scan(&count)
	if err != nil {
		log.Printf("DB error: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "DB error",
			Code:  "DBError",
		})
		return
	}

	if count == 0 {
		// Создаем профиль
		const queryInsert = `
			INSERT INTO profile (user_id, first_name, middle_name, last_name, email, phone)
			VALUES ($1, $2, $3, $4, $5, $6)
		`
		_, err := database.DB.Exec(queryInsert, userID, input.FirstName, input.MiddleName, input.LastName, input.Email, input.Phone)
		if err != nil {
			log.Printf("DB insert error: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(models.ErrorResponse{
				Error: "Failed to create profile",
				Code:  "CreateProfileError",
			})
			return
		}
	} else {
		// Обновляем профиль
		const queryUpdate = `
			UPDATE profile
			SET first_name=$1,
				middle_name=$2,
				last_name=$3,
				email=$4,
				phone=$5
			WHERE user_id=$6
		`
		_, err := database.DB.Exec(queryUpdate, input.FirstName, input.MiddleName, input.LastName, input.Email, input.Phone, userID)
		if err != nil {
			log.Printf("DB update error: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(models.ErrorResponse{
				Error: "Failed to update profile",
				Code:  "UpdateProfileError",
			})
			return
		}
	}

	// 7. Возвращаем успешный ответ
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(struct {
		Message string `json:"message"`
	}{
		Message: "Profile saved successfully",
	})
}

func UpdateProfileHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Method not allowed",
			Code:  "MethodNotAllowed",
		})
		return
	}

	// 1. Парсим токен из заголовка Authorization
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Authorization header missing",
			Code:  "AuthorizationHeaderMissing",
		})
		return
	}

	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || parts[0] != "Bearer" {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Invalid authorization header",
			Code:  "InvalidAuthorizationHeader",
		})
		return
	}

	token := parts[1]

	// 2. Проверка валидности токена
	claims, err := utils.ParseToken(token)
	if err != nil {
		// Проверка на истекший токен
		if strings.Contains(err.Error(), "token is expired") {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(models.ErrorResponse{
				Error: "Token expired",
				Code:  "TokenExpired",
			})
			return
		}
		// Другие ошибки
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Invalid token",
			Code:  "InvalidToken",
		})
		return
	}

	// 3. Получаем user_id из claims
	userID, ok := claims["id"].(string)
	if !ok || userID == "" {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Invalid token payload",
			Code:  "InvalidTokenPayload",
		})
		return
	}

	// 4. парсим тело запроса с необязательными полями
	var input struct {
		FirstName  *string `json:"first_name"`
		MiddleName *string `json:"middle_name"`
		LastName   *string `json:"last_name"`
		Email      *string `json:"email"`
		Phone      *string `json:"phone"`
	}
	err = json.NewDecoder(r.Body).Decode(&input)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Invalid request body",
			Code:  "InvalidRequestBody",
		})
		return
	}

	// 5. Формируем динамический запрос для обновления только переданных полей
	// (более удобно делать через `sql`, чтобы избежать обновления NULL, если поле не передано)
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
		// Нечего обновлять
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "No fields to update",
			Code:  "NoFieldsToUpdate",
		})
		return
	}

	// Добавляем WHERE
	query := "UPDATE profile SET " + strings.Join(setClauses, ", ") + " WHERE user_id=$" + strconv.Itoa(argIdx)
	args = append(args, userID)

	// Выполняем запрос
	_, err = database.DB.Exec(query, args...)
	if err != nil {
		log.Printf("DB update error: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Failed to update profile",
			Code:  "UpdateProfileError",
		})
		return
	}

	// Возвращаем успешный ответ
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(struct {
		Message string `json:"message"`
	}{
		Message: "Profile updated successfully",
	})
}

func GetProfileHandler(w http.ResponseWriter, r *http.Request) {
	// Вспомогательная функция
	ptrToString := func(s *string) string {
		if s != nil {
			return *s
		}
		return ""
	}

	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Method not allowed",
			Code:  "MethodNotAllowed",
		})
		return
	}

	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Authorization header missing",
			Code:  "AuthorizationHeaderMissing",
		})
		return
	}

	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || parts[0] != "Bearer" {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Invalid authorization header",
			Code:  "InvalidAuthorizationHeader",
		})
		return
	}

	token := parts[1]
	claims, err := utils.ParseToken(token)
	if err != nil {
		if strings.Contains(err.Error(), "token is expired") {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(models.ErrorResponse{
				Error: "Token expired",
				Code:  "TokenExpired",
			})
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Invalid token",
			Code:  "InvalidToken",
		})
		return
	}

	userID, ok := claims["id"].(string)
	if !ok || userID == "" {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Invalid token payload",
			Code:  "InvalidTokenPayload",
		})
		return
	}

	// получаем профиль
	profile, err := database.GetProfileByUserID(userID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "DB error",
			Code:  "DBError",
		})
		return
	}
	if profile == nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Profile not found",
			Code:  "ProfileNotFound",
		})
		return
	}

	// получаем логин
	login, err := database.GetLoginByUserID(userID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Failed to get login",
			Code:  "DBError",
		})
		return
	}

	resp := struct {
		ID         string `json:"id"`
		FirstName  string `json:"first_name"`
		MiddleName string `json:"middle_name,omitempty"`
		LastName   string `json:"last_name,omitempty"`
		Email      string `json:"email,omitempty"`
		Phone      string `json:"phone,omitempty"`
		Login      string `json:"login"`
	}{
		ID:         profile.UserID,
		FirstName:  profile.FirstName,
		MiddleName: ptrToString(profile.MiddleName),
		LastName:   ptrToString(profile.LastName),
		Email:      ptrToString(profile.Email),
		Phone:      ptrToString(profile.Phone),
		Login:      login,
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

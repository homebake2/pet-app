package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"myauthservice/database"
	"myauthservice/models"
	"myauthservice/utils"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

func PetHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		GetAllPetHandler(w, r)
	case http.MethodPost:
		CreatePetHandler(w, r)
	case http.MethodDelete:
		DeletePetHandler(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Method not allowed",
			Code:  "MethodNotAllowed",
		})
	}
}

func PetByIDHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		GetPetHandler(w, r)
	case http.MethodPut:
		UpdatePetHandler(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Method not allowed",
			Code:  "MethodNotAllowed",
		})
	}
}

// CreatePetHandler обрабатывает POST /pet
func CreatePetHandler(w http.ResponseWriter, r *http.Request) {
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

	// 4. Парсим тело запроса
	var req models.CreatePetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResp := models.ErrorResponse{Error: "Invalid request body", Code: "INVALID_BODY"}
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(errorResp)
		return
	}

	// 5. Валидация поля Icon
	if req.Icon != nil && !models.IsValidIcon(*req.Icon) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Некорректное значение icon",
			Code:  "BAD_REQUEST",
		})
		return
	}
	profileID, err := database.GetProfileIDByUserID(userID)
	if err != nil {
		// Обработка ошибки
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Profile not found",
			Code:  "ProfileNotFound",
		})
		return
	}
	// 5. Создаем запись в таблице pet
	newPetID, err := database.InsertPet(profileID, req)
	if err != nil {
		// Обработка ошибок базы данных
		errorResp := models.ErrorResponse{Error: "Failed to create pet", Code: "DB_ERROR"}
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(errorResp)
		return
	}

	// 6. Возвращаем успех (опционально можно вернуть созданного питомца)
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(struct {
		ID uuid.UUID `json:"id"`
	}{
		ID: newPetID,
	})
}

// GetAllPetHandler обрабатывает GET /pet
func GetAllPetHandler(w http.ResponseWriter, r *http.Request) {
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

	// 3. Получаем profile_id по user_id
	profileID, err := database.GetProfileIDByUserID(userID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Failed to get profile",
			Code:  "PROFILE_NOT_FOUND",
		})
		return
	}

	// 4. Получаем питомцев по profile_id
	pets, err := database.GetPetsByProfileID(profileID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Failed to get pets",
			Code:  "DB_ERROR",
		})
		return
	}

	// 5. Формируем ответ
	response := models.PetResponse{
		Items: make([]models.PetItem, 0, len(pets)),
	}

	for _, pet := range pets {
		icon := "OTHER"

		if pet.Icon.Valid && pet.Icon.String != "" {
			icon = pet.Icon.String
		}

		response.Items = append(response.Items, models.PetItem{
			ID:      pet.ID.String(),
			Name:    pet.Name,
			Breed:   pet.Breed,
			Species: pet.Species,
			Icon:    icon,
		})
	}

	// Если массив пустой, возвращаем его как есть
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// GetPetHandler обрабатывает GET /pet/{id}
func GetPetHandler(w http.ResponseWriter, r *http.Request) {
	// 1. Получаем ID питомца
	petIDStr := strings.TrimPrefix(r.URL.Path, "/pet/")
	petID, err := uuid.Parse(petIDStr)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Некорректный id питомца",
			Code:  "BAD_REQUEST",
		})
		return
	}

	// 2. Получаем Authorization header
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Отсутствует токен",
			Code:  "UNAUTHORIZED",
		})
		return
	}

	tokenParts := strings.Split(authHeader, "Bearer ")
	if len(tokenParts) != 2 {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Некорректный токен",
			Code:  "UNAUTHORIZED",
		})
		return
	}

	tokenStr := tokenParts[1]

	// 3. Парсим токен через utils
	claims, err := utils.ParseToken(tokenStr)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Токен истек",
			Code:  "UNAUTHORIZED",
		})
		return
	}

	// 4. Достаём user_id
	userIDRaw, ok := claims["id"]
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Невалидный токен",
			Code:  "UNAUTHORIZED",
		})
		return
	}

	userID, ok := userIDRaw.(string)
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Невалидный токен",
			Code:  "UNAUTHORIZED",
		})
		return
	}

	// 5. Получаем profile_id
	profileID, err := database.GetProfileIDByUserID(userID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Ошибка получения профиля",
			Code:  "DB_ERROR",
		})
		return
	}

	pet, err := database.GetPetByIDAndProfileID(petID, profileID)

	if err == sql.ErrNoRows {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: fmt.Sprintf("У данного пользователя нет питомца %s", petID.String()),
			Code:  "Недостаточно прав",
		})
		return
	}

	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Ошибка получения питомца",
			Code:  "DB_ERROR",
		})
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(pet)
}

func UpdatePetHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	petIDStr := strings.TrimPrefix(r.URL.Path, "/pet/")
	petID, err := uuid.Parse(petIDStr)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Некорректный id питомца",
			Code:  "BAD_REQUEST",
		})
		return
	}

	// токен
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Отсутствует токен",
			Code:  "UNAUTHORIZED",
		})
		return
	}

	token := strings.TrimPrefix(authHeader, "Bearer ")

	claims, err := utils.ParseToken(token)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Токен истек",
			Code:  "UNAUTHORIZED",
		})
		return
	}

	userID := claims["id"].(string)

	profileID, err := database.GetProfileIDByUserID(userID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Ошибка профиля",
			Code:  "DB_ERROR",
		})
		return
	}

	// проверка владельца
	_, err = database.GetPetByIDAndProfileID(petID, profileID)
	if err != nil {
		if err == sql.ErrNoRows {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(models.ErrorResponse{
				Error: fmt.Sprintf("у данного пользователя нет питомца %s", petID),
				Code:  "Недостаточно прав",
			})
			return
		}

		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Ошибка получения питомца",
			Code:  "DB_ERROR",
		})
		return
	}

	// decode body
	var req models.UpdatePetRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Некорректный JSON",
			Code:  "BAD_REQUEST",
		})
		return
	}

	// Валидация поля Icon
	if req.Icon != nil && !models.IsValidIcon(*req.Icon) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Некорректное значение icon",
			Code:  "BAD_REQUEST",
		})
		return
	}
	log.Printf("REQ: %+v\n", req)
	// update
	err = database.UpdatePet(petID, profileID, req)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Ошибка обновления питомца",
			Code:  "DB_ERROR",
		})
		return
	}

	w.WriteHeader(http.StatusOK)
}

func DeletePetHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// 1. Проверка метода
	if r.Method != http.MethodDelete {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Method not allowed",
			Code:  "MethodNotAllowed",
		})
		return
	}

	// 2. Токен
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Отсутствует токен",
			Code:  "UNAUTHORIZED",
		})
		return
	}

	token := strings.TrimPrefix(authHeader, "Bearer ")

	claims, err := utils.ParseToken(token)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Токен истек",
			Code:  "UNAUTHORIZED",
		})
		return
	}

	// 3. user_id
	userIDRaw, ok := claims["id"]
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Невалидный токен",
			Code:  "UNAUTHORIZED",
		})
		return
	}

	userID, ok := userIDRaw.(string)
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Невалидный токен",
			Code:  "UNAUTHORIZED",
		})
		return
	}

	// 4. profile_id
	profileID, err := database.GetProfileIDByUserID(userID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Ошибка профиля",
			Code:  "DB_ERROR",
		})
		return
	}

	// 5. body
	var req struct {
		ID string `json:"id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Некорректный JSON или отсутствует id",
			Code:  "BAD_REQUEST",
		})
		return
	}

	petID, err := uuid.Parse(req.ID)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Некорректный id питомца",
			Code:  "BAD_REQUEST",
		})
		return
	}

	// 6. Проверка владельца
	_, err = database.GetPetByIDAndProfileID(petID, profileID)
	if err != nil {
		if err == sql.ErrNoRows {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(models.ErrorResponse{
				Error: fmt.Sprintf("у данного пользователя нет питомца %s", petID),
				Code:  "Недостаточно прав",
			})
			return
		}

		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Ошибка получения питомца",
			Code:  "DB_ERROR",
		})
		return
	}

	// 7. Удаление (soft delete)
	err = database.DeletePet(petID, profileID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Ошибка удаления питомца",
			Code:  "DB_ERROR",
		})
		return
	}

	// 8. OK
	w.WriteHeader(http.StatusOK)
}

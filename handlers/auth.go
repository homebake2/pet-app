package handlers

import (
	"database/sql"
	"encoding/json"
	"log"
	"myauthservice/database"
	"myauthservice/models"
	"myauthservice/utils"
	"net/http"
	"time"

	"github.com/google/uuid"
)

func RegisterHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("Обработчик /auth/register вызван")
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var user models.User
	err := json.NewDecoder(r.Body).Decode(&user)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Проверка, есть ли пользователь уже
	var exists bool
	err = database.DB.QueryRow("SELECT EXISTS(SELECT 1 FROM users WHERE login=$1)", user.Login).Scan(&exists)
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	if exists {
		http.Error(w, "Пользователь уже существует", http.StatusConflict)
		return
	}

	// Вставка нового пользователя и получение его uuid
	var userId uuid.UUID
	err = database.DB.QueryRow(
		"INSERT INTO users (login, password) VALUES ($1, $2) RETURNING id",
		user.Login, user.Password,
	).Scan(&userId)
	if err != nil {
		// Обработка ошибки
		http.Error(w, "Ошибка при вставке пользователя", http.StatusInternalServerError)
		return
	}

	// Генерация токенов
	accessToken, err := utils.GenerateToken(user.Login, userId.String(), "access", 14*24*time.Hour)
	if err != nil {
		http.Error(w, "Failed to generate access token", http.StatusInternalServerError)
		return
	}
	refreshToken, err := utils.GenerateToken(user.Login, userId.String(), "refresh", 60*24*time.Hour)
	if err != nil {
		http.Error(w, "Failed to generate refresh token", http.StatusInternalServerError)
		return
	}

	// Обновляем таблицу для сохранения refresh_token
	_, err = database.DB.Exec("UPDATE users SET refresh_token=$1 WHERE login=$2", refreshToken, user.Login)
	if err != nil {
		log.Printf("Ошибка при сохранении refresh_token: %v", err)
		// Можно оставить продолжение, если не критично
	}

	resp := models.AuthResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func LoginHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req models.User
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Некорректный запрос", http.StatusBadRequest)
		return
	}

	// Ищем пользователя по логину
	var userPassword string
	var userId uuid.UUID
	err := database.DB.QueryRow("SELECT id, password FROM users WHERE login=$1", req.Login).Scan(&userId, &userPassword)
	if err == sql.ErrNoRows {
		// Пользователь не найден
	}

	// Проверяем пароль
	if userPassword != req.Password {
		// Неверный пароль
	}

	accessToken, err := utils.GenerateToken(req.Login, userId.String(), "access", 14*24*time.Hour)
	refreshToken, err := utils.GenerateToken(req.Login, userId.String(), "refresh", 60*24*time.Hour)

	_, err = database.DB.Exec("UPDATE users SET refresh_token=$1 WHERE login=$2", refreshToken, req.Login)
	if err != nil {
		log.Printf("Ошибка при обновлении refresh_token: %v", err)
		// Можно решить: продолжать ли или возвращать ошибку, но лучше логировать
	}

	resp := models.AuthResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

func RefreshTokenHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var input struct {
		RefreshToken string `json:"refresh_token"`
	}

	err := json.NewDecoder(r.Body).Decode(&input)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Некорректный запрос",
			Code:  "Некорректный запрос",
		})
		return
	}

	if input.RefreshToken == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Отсутствует refresh_token",
			Code:  "Отсутствует refresh_token",
		})
		return
	}

	// Распарсить и проверить токен
	claims, err := utils.ParseToken(input.RefreshToken)
	if err != nil {
		// обработка ошибки
	}

	// извлечение типа токена
	claimType, ok := claims["type"].(string)
	if !ok || claimType != "refresh" {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Некорректный refresh token",
			Code:  "Некорректный refresh token",
		})
		return
	}

	login := claims["login"].(string)
	userIdStr := claims["user_id"].(string) // или claims["id"], если так хранится

	// преобразуем из строки в uuid.UUID, если нужно (можете оставить строку, если utils умеет принимать string)
	userId, err := uuid.Parse(userIdStr)
	if err != nil {
		// обработка ошибки
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Некорректный user ID в токене",
			Code:  "Некорректный user ID",
		})
		return
	}

	// Проверка, что токен совпадает с сохраненным (если есть такая логика)
	var storedRefreshToken string
	err = database.DB.QueryRow("SELECT refresh_token FROM users WHERE login=$1", login).Scan(&storedRefreshToken)
	if err == sql.ErrNoRows {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Пользователь не найден",
			Code:  "Пользователь не найден",
		})
		return
	} else if err != nil {
		log.Printf("Ошибка при поиске пользователя: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Внутренняя ошибка сервера",
			Code:  "Внутренняя ошибка",
		})
		return
	}

	if storedRefreshToken != input.RefreshToken {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Несовпадение токена",
			Code:  "Несовпадение токена",
		})
		return
	}

	// Генерация новых токенов
	accessToken, err := utils.GenerateToken(login, userId.String(), "access", 14*24*time.Hour)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Ошибка генерации access токена",
			Code:  "Генерация токена",
		})
		return
	}

	refreshToken, err := utils.GenerateToken(login, userId.String(), "refresh", 60*24*time.Hour)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Ошибка генерации refresh токена",
			Code:  "Генерация токена",
		})
		return
	}

	// Обновляем refresh токен в базе
	_, err = database.DB.Exec("UPDATE users SET refresh_token=$1 WHERE login=$2", refreshToken, login)
	if err != nil {
		log.Printf("Ошибка обновления refresh токена: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(models.ErrorResponse{
			Error: "Ошибка обновления токена",
			Code:  "Обновление токена",
		})
		return
	}

	// Отправляем ответ
	json.NewEncoder(w).Encode(models.AuthResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	})
}

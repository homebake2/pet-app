package handlers

import (
	"database/sql"
	"encoding/json"
	"log"
	"myauthservice/database"
	"myauthservice/models"
	"myauthservice/openapi"
	"myauthservice/utils"
	"net/http"
	"time"

	"github.com/google/uuid"
)

func RegisterHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("Обработчик /auth/register вызван")
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
		return
	}

	var user models.User
	if err := json.NewDecoder(r.Body).Decode(&user); err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректное тело запроса")
		return
	}

	if user.Login == "" || user.Password == "" {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поля login и password обязательны")
		return
	}

	var exists bool
	if err := database.DB.QueryRow("SELECT EXISTS(SELECT 1 FROM users WHERE login=$1)", user.Login).Scan(&exists); err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка базы данных")
		return
	}
	if exists {
		writeError(w, http.StatusConflict, openapi.CONFLICT, "Пользователь с таким login уже существует")
		return
	}

	var userId uuid.UUID
	err := database.DB.QueryRow(
		"INSERT INTO users (login, password) VALUES ($1, $2) RETURNING id",
		user.Login, user.Password,
	).Scan(&userId)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка при создании пользователя")
		return
	}

	accessToken, err := utils.GenerateToken(user.Login, userId.String(), "access", 14*24*time.Hour)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось сгенерировать access token")
		return
	}
	refreshToken, err := utils.GenerateToken(user.Login, userId.String(), "refresh", 60*24*time.Hour)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось сгенерировать refresh token")
		return
	}

	if _, err := database.DB.Exec("UPDATE users SET refresh_token=$1 WHERE login=$2", refreshToken, user.Login); err != nil {
		log.Printf("Ошибка при сохранении refresh_token: %v", err)
	}

	writeJSON(w, http.StatusOK, models.AuthResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	})
}

func LoginHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
		return
	}

	var req models.User
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректный запрос")
		return
	}

	if req.Login == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поля login и password обязательны")
		return
	}

	var userPassword string
	var userId uuid.UUID
	err := database.DB.QueryRow("SELECT id, password FROM users WHERE login=$1", req.Login).Scan(&userId, &userPassword)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Пользователь не найден")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка базы данных")
		return
	}

	if userPassword != req.Password {
		writeError(w, http.StatusForbidden, openapi.FORBIDDEN, "Неверный пароль")
		return
	}

	accessToken, err := utils.GenerateToken(req.Login, userId.String(), "access", 14*24*time.Hour)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось сгенерировать access token")
		return
	}
	refreshToken, err := utils.GenerateToken(req.Login, userId.String(), "refresh", 60*24*time.Hour)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось сгенерировать refresh token")
		return
	}

	if _, err := database.DB.Exec("UPDATE users SET refresh_token=$1 WHERE login=$2", refreshToken, req.Login); err != nil {
		log.Printf("Ошибка при обновлении refresh_token: %v", err)
	}

	writeJSON(w, http.StatusOK, models.AuthResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	})
}

func RefreshTokenHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
		return
	}

	var input struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректный запрос")
		return
	}

	if input.RefreshToken == "" {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Отсутствует refresh_token")
		return
	}

	claims, err := utils.ParseToken(input.RefreshToken)
	if err != nil {
		writeError(w, http.StatusUnauthorized, openapi.UNAUTHORIZED, "Невалидный или истёкший refresh token")
		return
	}

	if claimType, ok := claims["type"].(string); !ok || claimType != "refresh" {
		writeError(w, http.StatusUnauthorized, openapi.UNAUTHORIZED, "Некорректный тип токена")
		return
	}

	login, ok := claims["login"].(string)
	if !ok || login == "" {
		writeError(w, http.StatusUnauthorized, openapi.UNAUTHORIZED, "Невалидный refresh token")
		return
	}

	userIdStr, ok := claims["id"].(string)
	if !ok || userIdStr == "" {
		writeError(w, http.StatusUnauthorized, openapi.UNAUTHORIZED, "Невалидный refresh token")
		return
	}

	userId, err := uuid.Parse(userIdStr)
	if err != nil {
		writeError(w, http.StatusUnauthorized, openapi.UNAUTHORIZED, "Невалидный user id в токене")
		return
	}

	var storedRefreshToken string
	err = database.DB.QueryRow("SELECT refresh_token FROM users WHERE login=$1", login).Scan(&storedRefreshToken)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusUnauthorized, openapi.UNAUTHORIZED, "Пользователь не найден")
		return
	}
	if err != nil {
		log.Printf("Ошибка при поиске пользователя: %v", err)
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Внутренняя ошибка сервера")
		return
	}

	if storedRefreshToken != input.RefreshToken {
		writeError(w, http.StatusUnauthorized, openapi.UNAUTHORIZED, "Токен отозван или устарел")
		return
	}

	accessToken, err := utils.GenerateToken(login, userId.String(), "access", 14*24*time.Hour)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка генерации access токена")
		return
	}

	refreshToken, err := utils.GenerateToken(login, userId.String(), "refresh", 60*24*time.Hour)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка генерации refresh токена")
		return
	}

	if _, err := database.DB.Exec("UPDATE users SET refresh_token=$1 WHERE login=$2", refreshToken, login); err != nil {
		log.Printf("Ошибка обновления refresh токена: %v", err)
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка обновления токена")
		return
	}

	writeJSON(w, http.StatusOK, models.AuthResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	})
}

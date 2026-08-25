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
	"golang.org/x/crypto/bcrypt"
)

// RegisterHandler и LoginHandler объединены: если пользователь с таким login
// уже существует — выполняется попытка входа (проверка пароля), если нет —
// пользователь автоматически регистрируется.
func RegisterHandler(w http.ResponseWriter, r *http.Request) {
	authenticateOrRegister(w, r)
}

func LoginHandler(w http.ResponseWriter, r *http.Request) {
	authenticateOrRegister(w, r)
}

func authenticateOrRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
		return
	}

	var req models.User
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректное тело запроса")
		return
	}

	if req.Login == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поля login и password обязательны")
		return
	}

	if len(req.Password) < 8 {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Пароль должен быть не короче 8 символов")
		return
	}

	var userId uuid.UUID
	var passwordHash string
	err := database.DB.QueryRow("SELECT id, password FROM users WHERE login=$1", req.Login).Scan(&userId, &passwordHash)
	switch {
	case err == sql.ErrNoRows:
		userId, err = createUser(req.Login, req.Password)
		if err != nil {
			writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка при создании пользователя")
			return
		}
	case err != nil:
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка базы данных")
		return
	default:
		if bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(req.Password)) != nil {
			writeError(w, http.StatusForbidden, openapi.FORBIDDEN, "Неверный пароль")
			return
		}
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
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось сохранить refresh token")
		return
	}

	writeJSON(w, http.StatusOK, models.AuthResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	})
}

// createUser хеширует пароль и создаёт новую запись пользователя.
func createUser(login, password string) (uuid.UUID, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return uuid.UUID{}, err
	}

	var userId uuid.UUID
	err = database.DB.QueryRow(
		"INSERT INTO users (login, password) VALUES ($1, $2) RETURNING id",
		login, string(hash),
	).Scan(&userId)
	return userId, err
}

func LogoutHandler(w http.ResponseWriter, r *http.Request) {
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

	if err := database.ClearRefreshToken(login); err != nil {
		log.Printf("Ошибка при инвалидации refresh_token: %v", err)
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось выполнить выход")
		return
	}

	w.WriteHeader(http.StatusNoContent)
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

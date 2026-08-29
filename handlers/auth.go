package handlers

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"myauthservice/database"
	"myauthservice/models"
	"myauthservice/openapi"
	"myauthservice/utils"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// unauthorizedLogoutMessage — единое сообщение для всех случаев отказа при
// logout (пользователь не найден, токен невалиден/истёк, токен отозван или
// устарел). Различать причину клиенту не нужно и не следует — это раскрывало
// бы детали, которые можно использовать для перебора логинов/токенов.
// Настоящая причина логируется на сервере через log.Printf в местах вызова.
const unauthorizedLogoutMessage = "Недействительный refresh token"

// maxRegistrationsPerIPPerDay — сколько авто-регистраций с одного IP
// допускается за сутки, прежде чем /auth/login начнёт отвечать 429.
const maxRegistrationsPerIPPerDay = 3

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

	// Логин нормализуется: обрезаются пробелы, сравнение с уже
	// существующими пользователями регистронезависимо (см.
	// database.FindUserByLoginNormalized и миграцию
	// 000003_login_case_insensitive_unique).
	req.Login = strings.TrimSpace(req.Login)

	if req.Login == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поля login и password обязательны")
		return
	}

	if len(req.Login) < 2 {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Login должен быть не короче 2 символов")
		return
	}

	if len(req.Password) < 8 {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Пароль должен быть не короче 8 символов")
		return
	}

	userId, passwordHash, err := database.FindUserByLoginNormalized(req.Login)
	switch {
	case err == sql.ErrNoRows:
		userId, err = registerNewUser(w, r, req.Login, req.Password)
		if err != nil {
			// Ответ клиенту уже отправлен внутри registerNewUser.
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

	if err := database.UpdateRefreshTokenByID(userId.String(), refreshToken); err != nil {
		log.Printf("Ошибка при обновлении refresh_token: %v", err)
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось сохранить refresh token")
		return
	}

	writeJSON(w, http.StatusOK, models.AuthResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	})
}

// registerNewUser обрабатывает ветку "пользователь не найден" в
// authenticateOrRegister: применяет rate-limit по IP на авто-регистрацию,
// затем создаёт пользователя. При гонке параллельных регистраций с одним и
// тем же login (unique_violation, код 23505) не считает это ошибкой сервера,
// а откатывается на сценарий "пользователь уже существует" — повторно ищет
// пользователя и проверяет пароль, как и обычный вход.
// При ошибке сама пишет ответ клиенту и возвращает err != nil, чтобы
// вызывающая сторона просто сделала `return`.
func registerNewUser(w http.ResponseWriter, r *http.Request, login, password string) (uuid.UUID, error) {
	ip := clientIP(r)
	count, err := database.UpsertRegistrationAttempt(ip)
	if err != nil {
		// Fail closed: при ошибке БД на шаге rate-limit регистрация не должна
		// продолжаться.
		log.Printf("Ошибка проверки лимита регистрации для IP %s: %v", ip, err)
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка базы данных")
		return uuid.UUID{}, err
	}
	if count > maxRegistrationsPerIPPerDay {
		writeError(w, http.StatusTooManyRequests, openapi.RATELIMITED, "Превышен лимит регистраций с этого IP за сутки")
		return uuid.UUID{}, errRateLimited
	}

	userId, err := createUser(login, password)
	if err != nil {
		if database.IsUniqueViolation(err) {
			// Параллельный запрос успел создать пользователя с тем же
			// (нормализованным) login первым — обрабатываем как обычный вход.
			existingId, passwordHash, lookupErr := database.FindUserByLoginNormalized(login)
			if lookupErr != nil {
				log.Printf("Ошибка повторного поиска пользователя после unique_violation: %v", lookupErr)
				writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка базы данных")
				return uuid.UUID{}, lookupErr
			}
			if bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)) != nil {
				writeError(w, http.StatusForbidden, openapi.FORBIDDEN, "Неверный пароль")
				return uuid.UUID{}, errWrongPassword
			}
			return existingId, nil
		}

		log.Printf("Ошибка при создании пользователя: %v", err)
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка при создании пользователя")
		return uuid.UUID{}, err
	}

	return userId, nil
}

// errRateLimited и errWrongPassword — внутренние сентинелы, чтобы
// registerNewUser мог сообщить вызывающей стороне "ответ уже отправлен,
// просто return", не описывая причину повторно. Конкретное значение не
// анализируется — важен только факт err != nil.
var (
	errRateLimited   = errors.New("registration rate limited")
	errWrongPassword = errors.New("wrong password on unique_violation fallback")
)

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
		log.Printf("Logout: невалидный или истёкший refresh token: %v", err)
		writeError(w, http.StatusUnauthorized, openapi.UNAUTHORIZED, unauthorizedLogoutMessage)
		return
	}

	if claimType, ok := claims["type"].(string); !ok || claimType != "refresh" {
		log.Printf("Logout: некорректный тип токена в claims: %v", claims["type"])
		writeError(w, http.StatusUnauthorized, openapi.UNAUTHORIZED, unauthorizedLogoutMessage)
		return
	}

	userIdStr, ok := claims["id"].(string)
	if !ok || userIdStr == "" {
		log.Printf("Logout: отсутствует id в claims")
		writeError(w, http.StatusUnauthorized, openapi.UNAUTHORIZED, unauthorizedLogoutMessage)
		return
	}

	userId, err := uuid.Parse(userIdStr)
	if err != nil {
		log.Printf("Logout: невалидный user id в токене: %v", err)
		writeError(w, http.StatusUnauthorized, openapi.UNAUTHORIZED, unauthorizedLogoutMessage)
		return
	}

	// Атомарный compare-and-clear: одним UPDATE проверяем и сбрасываем
	// refresh_token, без гонки между отдельными SELECT и UPDATE под
	// параллельными logout/refresh с одним и тем же токеном.
	cleared, err := database.ClearRefreshTokenByIDIfMatches(userId.String(), input.RefreshToken)
	if err != nil {
		log.Printf("Ошибка при инвалидации refresh_token: %v", err)
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось выполнить выход")
		return
	}
	if !cleared {
		// Пользователь не найден ИЛИ сохранённый токен не совпал (отозван/
		// устарел) — с точки зрения клиента это один и тот же ответ.
		log.Printf("Logout: пользователь %s не найден или refresh token не совпал", userId)
		writeError(w, http.StatusUnauthorized, openapi.UNAUTHORIZED, unauthorizedLogoutMessage)
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

	// Атомарная ротация: одним UPDATE проверяем, что сохранённый
	// refresh_token совпадает со старым, и сразу заменяем его на новый — без
	// отдельного SELECT, который создавал бы гонку под параллельными
	// refresh-вызовами с одним и тем же токеном.
	rotated, err := database.RotateRefreshTokenByID(userId.String(), input.RefreshToken, refreshToken)
	if err != nil {
		log.Printf("Ошибка обновления refresh токена: %v", err)
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка обновления токена")
		return
	}
	if !rotated {
		writeError(w, http.StatusUnauthorized, openapi.UNAUTHORIZED, "Токен отозван, устарел или пользователь не найден")
		return
	}

	writeJSON(w, http.StatusOK, models.AuthResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	})
}

// GuestHandler реализует POST /auth/guest: создаёт или находит гостевого
// пользователя по device_id и выдаёт ему обычную пару токенов. Повторный
// вызов с тем же device_id идемпотентен — новая запись не создаётся, отдаётся
// новая пара токенов для уже существующего гостевого пользователя.
func GuestHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
		return
	}

	var req openapi.GetGuestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректное тело запроса")
		return
	}

	deviceID := strings.TrimSpace(req.DeviceId)
	if deviceID == "" {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поле device_id обязательно")
		return
	}

	userId, login, err := database.FindUserIDAndLoginByGuestDeviceID(deviceID)
	switch {
	case err == sql.ErrNoRows:
		login, err = generateGuestLogin()
		if err != nil {
			log.Printf("Ошибка генерации guest login: %v", err)
			writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось создать гостевого пользователя")
			return
		}
		userId, err = database.CreateGuestUser(login, deviceID)
		if err != nil {
			log.Printf("Ошибка создания гостевого пользователя: %v", err)
			writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось создать гостевого пользователя")
			return
		}
	case err != nil:
		log.Printf("Ошибка поиска гостевого пользователя: %v", err)
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка базы данных")
		return
	}

	accessToken, err := utils.GenerateToken(login, userId.String(), "access", 14*24*time.Hour)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось сгенерировать access token")
		return
	}
	refreshToken, err := utils.GenerateToken(login, userId.String(), "refresh", 60*24*time.Hour)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось сгенерировать refresh token")
		return
	}

	if err := database.UpdateRefreshTokenByID(userId.String(), refreshToken); err != nil {
		log.Printf("Ошибка при обновлении refresh_token гостя: %v", err)
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось сохранить refresh token")
		return
	}

	writeJSON(w, http.StatusOK, models.AuthResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	})
}

// generateGuestLogin генерирует login вида guest_<32 hex символа> через
// crypto/rand.
func generateGuestLogin() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "guest_" + hex.EncodeToString(buf), nil
}

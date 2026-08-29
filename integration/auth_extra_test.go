//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Login нормализуется: пробелы обрезаются, сравнение регистронезависимо —
// поэтому повторная "регистрация" с другим регистром/пробелами на самом деле
// логинит того же пользователя, а не создаёт нового.
func TestAuthFlow_LoginNormalizedTrimAndCase(t *testing.T) {
	resetDB(t)

	base := fmt.Sprintf("normuser-%s", uuid.NewString())
	registerUser(t, base, "correct-password")

	sameUserDifferentCase := doRequest(t, http.MethodPost, "/auth/login", map[string]string{
		"login":    "  " + base + "  ",
		"password": "correct-password",
	}, "")
	require.Equal(t, http.StatusOK, sameUserDifferentCase.status)

	wrongPasswordDifferentCase := doRequest(t, http.MethodPost, "/auth/login", map[string]string{
		"login":    base,
		"password": "totally-wrong",
	}, "")
	require.Equal(t, http.StatusForbidden, wrongPasswordDifferentCase.status)
}

func TestAuthFlow_LoginMinLength(t *testing.T) {
	resetDB(t)

	resp := doRequest(t, http.MethodPost, "/auth/register", map[string]string{
		"login":    "a",
		"password": "correct-password",
	}, "")
	require.Equal(t, http.StatusBadRequest, resp.status)
	var body struct {
		Code string `json:"code"`
	}
	resp.decode(t, &body)
	require.Equal(t, "VALIDATION_ERROR", body.Code)
}

// Logout не должен раскрывать причину отказа: и неизвестный/просроченный
// токен, и уже отозванный дают один и тот же код/сообщение.
func TestAuthFlow_LogoutUnifiedErrorMessage(t *testing.T) {
	resetDB(t)

	tokens := registerUser(t, uniqueLogin(t), "correct-password")

	loggedOut := doRequest(t, http.MethodPost, "/auth/logout", map[string]string{
		"refresh_token": tokens.RefreshToken,
	}, "")
	require.Equal(t, http.StatusNoContent, loggedOut.status)

	// Повторный logout тем же (уже отозванным) refresh_token.
	staleResp := doRequest(t, http.MethodPost, "/auth/logout", map[string]string{
		"refresh_token": tokens.RefreshToken,
	}, "")
	require.Equal(t, http.StatusUnauthorized, staleResp.status)
	var staleBody struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	staleResp.decode(t, &staleBody)

	garbageResp := doRequest(t, http.MethodPost, "/auth/logout", map[string]string{
		"refresh_token": "not-a-jwt",
	}, "")
	require.Equal(t, http.StatusUnauthorized, garbageResp.status)
	var garbageBody struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	garbageResp.decode(t, &garbageBody)

	require.Equal(t, staleBody.Code, garbageBody.Code)
	require.Equal(t, staleBody.Message, garbageBody.Message)
}

func TestGuestAuth_CreatesAndReusesIdempotently(t *testing.T) {
	resetDB(t)

	deviceID := "device-" + uuid.NewString()

	first := doRequest(t, http.MethodPost, "/auth/guest", map[string]string{
		"device_id": deviceID,
	}, "")
	require.Equal(t, http.StatusOK, first.status)
	var firstTokens authTokens
	first.decode(t, &firstTokens)
	require.NotEmpty(t, firstTokens.AccessToken)

	// Второй вызов с тем же device_id не должен создавать новую запись —
	// он идемпотентен и возвращает валидную пару токенов для того же гостя.
	second := doRequest(t, http.MethodPost, "/auth/guest", map[string]string{
		"device_id": deviceID,
	}, "")
	require.Equal(t, http.StatusOK, second.status)
	var secondTokens authTokens
	second.decode(t, &secondTokens)
	require.NotEmpty(t, secondTokens.AccessToken)

	// Оба вызова относятся к одному и тому же (не продублированному) гостевому
	// пользователю: профиль, созданный под токеном первого вызова, виден и
	// через токен второго.
	created := doRequest(t, http.MethodPost, "/profile", map[string]string{
		"first_name": "Guest",
	}, firstTokens.AccessToken)
	require.Equal(t, http.StatusNoContent, created.status)

	got := doRequest(t, http.MethodGet, "/profile", nil, secondTokens.AccessToken)
	require.Equal(t, http.StatusOK, got.status)
	var profile struct {
		FirstName string `json:"first_name"`
	}
	got.decode(t, &profile)
	require.Equal(t, "Guest", profile.FirstName)
}

// device_id, состоящий только из пробелов, проходит валидацию OpenAPI-схемы
// (minLength: 1), но после обрезки пробелов на сервере должен считаться
// отсутствующим.
func TestGuestAuth_BlankDeviceIDAfterTrim(t *testing.T) {
	resetDB(t)

	resp := doRequest(t, http.MethodPost, "/auth/guest", map[string]string{
		"device_id": "   ",
	}, "")
	require.Equal(t, http.StatusBadRequest, resp.status)
	var body struct {
		Code string `json:"code"`
	}
	resp.decode(t, &body)
	require.Equal(t, "VALIDATION_ERROR", body.Code)
}

// Более 3 авто-регистраций с одного IP за сутки должны получить 429
// RATE_LIMITED вместо создания очередного аккаунта.
func TestAuthFlow_RegistrationRateLimitedPerIP(t *testing.T) {
	resetDB(t)

	for i := 0; i < 3; i++ {
		resp := doRequest(t, http.MethodPost, "/auth/register", map[string]string{
			"login":    fmt.Sprintf("ratelimit-%s", uuid.NewString()),
			"password": "correct-password",
		}, "")
		require.Equalf(t, http.StatusOK, resp.status, "регистрация #%d должна пройти: %s", i+1, resp.body)
	}

	blocked := doRequest(t, http.MethodPost, "/auth/register", map[string]string{
		"login":    fmt.Sprintf("ratelimit-%s", uuid.NewString()),
		"password": "correct-password",
	}, "")
	require.Equalf(t, http.StatusTooManyRequests, blocked.status, "%s", blocked.body)
	var body struct {
		Code string `json:"code"`
	}
	blocked.decode(t, &body)
	require.Equal(t, "RATE_LIMITED", body.Code)
}

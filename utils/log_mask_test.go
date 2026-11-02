package utils

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func maskedMap(t *testing.T, body string) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(MaskBodyForLog([]byte(body))), &out))
	return out
}

func TestMaskBodyForLog_MasksCredentialsAndTokens(t *testing.T) {
	out := maskedMap(t, `{"login":"john","password":"secret-pw","access_token":"a.b.c","refreshToken":"d.e.f","device_id":"dev-1"}`)

	assert.Equal(t, "john", out["login"])
	assert.Equal(t, maskedValue, out["password"])
	assert.Equal(t, maskedValue, out["access_token"])
	assert.Equal(t, maskedValue, out["refreshToken"])
	assert.Equal(t, maskedValue, out["device_id"])
}

func TestMaskBodyForLog_MasksNestedAndArrays(t *testing.T) {
	out := maskedMap(t, `{"items":[{"id":"1","files":[{"url":"https://s3/x?X-Amz-Signature=abc"}]}],"upload_url":"https://s3/y"}`)

	assert.Equal(t, maskedValue, out["upload_url"])
	files := out["items"].([]any)[0].(map[string]any)["files"].([]any)[0].(map[string]any)
	assert.Equal(t, maskedValue, files["url"])
	assert.Equal(t, "1", out["items"].([]any)[0].(map[string]any)["id"])
}

func TestMaskBodyForLog_MasksTransferCodeButKeepsErrorCode(t *testing.T) {
	assert.Equal(t, maskedValue, maskedMap(t, `{"code":"TABCD234"}`)["code"])

	errBody := maskedMap(t, `{"code":"FORBIDDEN","message":"Неверный пароль"}`)
	assert.Equal(t, "FORBIDDEN", errBody["code"])
	assert.Equal(t, "Неверный пароль", errBody["message"])
}

func TestMaskBodyForLog_KeepsLargeNumbersAndEmptyBody(t *testing.T) {
	assert.JSONEq(t, `{"n":12345678901234567890}`, MaskBodyForLog([]byte(`{"n":12345678901234567890}`)))
	assert.Equal(t, "", MaskBodyForLog(nil))
	assert.Equal(t, "", MaskBodyForLog([]byte("  \n")))
}

func TestMaskBodyForLog_NonJSONIsNotLogged(t *testing.T) {
	masked := MaskBodyForLog([]byte("password=secret"))
	assert.NotContains(t, masked, "secret")
	assert.Contains(t, masked, "не JSON")
}

// В лог middleware не должны попадать пароль из запроса и токены из ответа.
func TestLoggingMiddleware_DoesNotLogSecrets(t *testing.T) {
	var buf bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(previous) })

	handler := LoggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"access_token":"tok-secret","refresh_token":"ref-secret","pets_count":1}`))
	}))
	req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"login":"john","password":"pw-secret"}`))
	req.Header.Set(AppVersionHeader, "1.4.0")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	logged := buf.String()
	assert.Contains(t, logged, "app_version=1.4.0")
	assert.NotContains(t, logged, "pw-secret")
	assert.NotContains(t, logged, "tok-secret")
	assert.NotContains(t, logged, "ref-secret")
	assert.Contains(t, logged, `"login":"john"`)
	assert.Contains(t, logged, `"pets_count":1`)
}

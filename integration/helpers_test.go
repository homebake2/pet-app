//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/stretchr/testify/require"
)

type apiResponse struct {
	status int
	header http.Header
	body   []byte
}

func (r apiResponse) decode(t *testing.T, v any) {
	t.Helper()
	require.NoErrorf(t, json.Unmarshal(r.body, v), "не удалось распарсить тело ответа: %s", r.body)
}

// findRoute ищет в спеке path item и operation, соответствующие методу и пути
// (без query-строки), и возвращает извлечённые path-параметры.
//
// Не используется routers/legacy: его роутер сверяет ещё и Host запроса со
// `servers` из спеки, а httptest.Server поднимается на случайном локальном
// порту — совпадения не будет. Здесь схема заведомо простая (максимум один
// параметр на сегмент пути), поэтому сопоставление шаблонов сделано вручную.
func findRoute(method, pathOnly string) (item *openapi3.PathItem, op *openapi3.Operation, pathParams map[string]string, template string) {
	for tmpl, candidate := range spec.Paths.Map() {
		params, ok := matchTemplate(tmpl, pathOnly)
		if !ok {
			continue
		}
		operation := candidate.GetOperation(method)
		if operation == nil {
			continue
		}
		// Шаблон без параметров (/events/stats) выигрывает у шаблона с
		// параметром (/events/{id}) — так же, как точный путь выигрывает у
		// поддерева в http.ServeMux. Иначе порядок обхода карты решал бы, по
		// какой схеме валидировать ответ.
		if len(params) == 0 {
			return candidate, operation, params, tmpl
		}
		if op == nil || len(params) < len(pathParams) {
			item, op, pathParams, template = candidate, operation, params, tmpl
		}
	}
	return item, op, pathParams, template
}

func matchTemplate(template, path string) (map[string]string, bool) {
	templateParts := strings.Split(strings.Trim(template, "/"), "/")
	pathParts := strings.Split(strings.Trim(path, "/"), "/")
	if len(templateParts) != len(pathParts) {
		return nil, false
	}

	params := map[string]string{}
	for i, part := range templateParts {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			params[strings.Trim(part, "{}")] = pathParts[i]
			continue
		}
		if part != pathParts[i] {
			return nil, false
		}
	}
	return params, true
}

// doRequest выполняет HTTP-запрос к тестовому серверу и сверяет и запрос,
// и ответ с контрактом из open-api/spec.json. path может содержать
// query-строку. extraHeaders — необязательный набор дополнительных
// заголовков запроса (например, Idempotency-Key); передаётся не более
// одной карты.
func doRequest(t *testing.T, method, path string, body any, token string, extraHeaders ...map[string]string) apiResponse {
	t.Helper()

	var bodyBytes []byte
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		bodyBytes = b
	}

	newReq := func() *http.Request {
		req, err := http.NewRequest(method, server.URL+path, bytes.NewReader(bodyBytes))
		require.NoError(t, err)
		if bodyBytes != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		for _, headers := range extraHeaders {
			for k, v := range headers {
				req.Header.Set(k, v)
			}
		}
		return req
	}

	pathOnly, _, _ := strings.Cut(path, "?")
	item, op, pathParams, template := findRoute(method, pathOnly)
	require.NotNilf(t, op, "в open-api/spec.json нет операции для %s %s", method, pathOnly)

	route := &routers.Route{Spec: spec, Path: template, PathItem: item, Method: method, Operation: op}
	reqValidationInput := &openapi3filter.RequestValidationInput{
		Request:    newReq(),
		PathParams: pathParams,
		Route:      route,
		// Аутентификация — забота хендлера (requireUserID), а не спеки; здесь
		// достаточно принять любой security requirement как выполненный,
		// иначе openapi3filter требует явный AuthenticationFunc.
		Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc},
	}

	ctx := context.Background()
	if err := openapi3filter.ValidateRequest(ctx, reqValidationInput); err != nil {
		t.Errorf("запрос %s %s не соответствует open-api/spec.json: %v", method, path, err)
	}

	resp, err := http.DefaultClient.Do(newReq())
	require.NoError(t, err)
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	respValidationInput := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: reqValidationInput,
		Status:                 resp.StatusCode,
		Header:                 resp.Header,
	}
	respValidationInput.SetBodyBytes(respBody)
	if err := openapi3filter.ValidateResponse(ctx, respValidationInput); err != nil {
		t.Errorf("ответ %s %s (статус %d) не соответствует open-api/spec.json: %v\nbody: %s", method, path, resp.StatusCode, err, respBody)
	}

	return apiResponse{status: resp.StatusCode, header: resp.Header.Clone(), body: respBody}
}

type authTokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// registerUser регистрирует нового пользователя со случайным логином и
// возвращает пару токенов.
func registerUser(t *testing.T, login, password string) authTokens {
	t.Helper()
	resp := doRequest(t, http.MethodPost, "/auth/register", map[string]string{
		"login":    login,
		"password": password,
	}, "")
	require.Equalf(t, http.StatusOK, resp.status, "регистрация не удалась: %s", resp.body)

	var tokens authTokens
	resp.decode(t, &tokens)
	require.NotEmpty(t, tokens.AccessToken)
	return tokens
}

// createProfile создаёт минимальный профиль для пользователя — требуется
// перед созданием питомцев (profile_id выводится из user_id).
func createProfile(t *testing.T, accessToken, firstName string) {
	t.Helper()
	resp := doRequest(t, http.MethodPost, "/profile", map[string]string{
		"first_name": firstName,
	}, accessToken)
	require.Equalf(t, http.StatusNoContent, resp.status, "создание профиля не удалось: %s", resp.body)
}

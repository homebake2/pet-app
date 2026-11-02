package utils

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// maskedValue подставляется вместо значения секретного поля в логах.
const maskedValue = "***"

// sensitiveKeys — поля тел запросов и ответов, значения которых нельзя
// писать в логи: учётные данные, токены, одноразовые коды и подписанные
// ссылки. Ключ сравнивается в нормализованном виде (см. normalizeLogKey), так
// что access_token и accessToken — одно и то же поле.
var sensitiveKeys = map[string]bool{
	"password":     true,
	"accesstoken":  true,
	"refreshtoken": true,
	"token":        true,
	"deviceid":     true, // идентификатор гостевого аккаунта = его учётные данные
	"code":         true, // код передачи/доступа к питомцу (кроме кода ошибки, см. maskValue)
}

// normalizeLogKey приводит имя поля к нижнему регистру без разделителей.
func normalizeLogKey(key string) string {
	key = strings.ToLower(key)
	key = strings.ReplaceAll(key, "_", "")
	return strings.ReplaceAll(key, "-", "")
}

// isSensitiveKey сообщает, нужно ли маскировать значение поля. Любое поле,
// заканчивающееся на "url", считается подписанной ссылкой (presigned
// upload/download) и тоже маскируется.
func isSensitiveKey(key string) bool {
	normalized := normalizeLogKey(key)
	return sensitiveKeys[normalized] || strings.HasSuffix(normalized, "url")
}

// maskValue рекурсивно заменяет значения секретных полей на maskedValue.
// Поле code в объекте с полем message — это код ошибки API
// ({code, message}), а не секретный код, и остаётся читаемым.
func maskValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		_, isError := v["message"]
		for key, inner := range v {
			if isSensitiveKey(key) && !(isError && normalizeLogKey(key) == "code") {
				v[key] = maskedValue
				continue
			}
			v[key] = maskValue(inner)
		}
		return v
	case []any:
		for i, inner := range v {
			v[i] = maskValue(inner)
		}
		return v
	default:
		return value
	}
}

// MaskBodyForLog возвращает тело запроса/ответа в виде, пригодном для логов:
// JSON с замаскированными секретными полями. Пустое тело остаётся пустым.
// Тело, которое не удалось разобрать как JSON, целиком заменяется
// описанием — безопаснее потерять диагностику, чем записать пароль или токен
// в нестандартном формате.
func MaskBodyForLog(body []byte) string {
	if len(bytes.TrimSpace(body)) == 0 {
		return ""
	}

	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber() // не терять точность больших чисел при обратной сериализации
	var parsed any
	if err := decoder.Decode(&parsed); err != nil {
		return fmt.Sprintf("<не JSON, %d байт>", len(body))
	}

	masked, err := json.Marshal(maskValue(parsed))
	if err != nil {
		return fmt.Sprintf("<не удалось замаскировать, %d байт>", len(body))
	}
	return string(masked)
}

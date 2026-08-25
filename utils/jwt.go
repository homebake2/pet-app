package utils

import (
	"errors"
	"os"
	"time"

	"github.com/golang-jwt/jwt"
)

// Секрет подписи токенов читается из JWT_SECRET; при отсутствии переменной
// окружения используется дефолтное значение (для локальной разработки).
var jwtKey = []byte(jwtSecretFromEnv())

func jwtSecretFromEnv() string {
	if secret := os.Getenv("JWT_SECRET"); secret != "" {
		return secret
	}
	return "ваш_секретный_ключ"
}

// Генерация токена с логином, id пользователя и типом
func GenerateToken(login string, userID string, tokenType string, duration time.Duration) (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"login": login,
		"id":    userID,
		"type":  tokenType,
		"iat":   now.Unix(),
		"exp":   now.Add(duration).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(jwtKey)
}

// Парсинг токена с получением данных
func ParseToken(tokenStr string) (map[string]interface{}, error) {
	token, err := jwt.Parse(tokenStr, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("неподдерживаемый метод подписи")
		}
		return jwtKey, nil
	})

	if err != nil {
		return nil, err
	}

	if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
		return claims, nil
	}
	return nil, errors.New("невалидный токен")
}

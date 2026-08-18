package utils

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt"
)

// Ваш секретный ключ (замените на свой)
var jwtKey = []byte("ваш_секретный_ключ")

// Генерация токена с логином, id пользователя и типом
func GenerateToken(login string, userID string, tokenType string, duration time.Duration) (string, error) {
	claims := jwt.MapClaims{
		"login": login,
		"id":    userID,
		"type":  tokenType,
		"exp":   time.Now().Add(duration).Unix(),
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

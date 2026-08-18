package utils

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateAndParseToken(t *testing.T) {
	token, err := GenerateToken("john", "user-123", "access", time.Hour)
	require.NoError(t, err)
	require.NotEmpty(t, token)

	claims, err := ParseToken(token)
	require.NoError(t, err)
	assert.Equal(t, "john", claims["login"])
	assert.Equal(t, "user-123", claims["id"])
	assert.Equal(t, "access", claims["type"])
}

func TestParseToken_Expired(t *testing.T) {
	token, err := GenerateToken("john", "user-123", "access", -time.Hour)
	require.NoError(t, err)

	_, err = ParseToken(token)
	assert.Error(t, err)
}

func TestParseToken_Malformed(t *testing.T) {
	_, err := ParseToken("not-a-jwt")
	assert.Error(t, err)
}

func TestParseToken_Empty(t *testing.T) {
	_, err := ParseToken("")
	assert.Error(t, err)
}

func TestParseToken_WrongSigningMethod(t *testing.T) {
	claims := jwt.MapClaims{
		"login": "john",
		"id":    "user-123",
		"type":  "access",
		"exp":   time.Now().Add(time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	signed, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)

	_, err = ParseToken(signed)
	assert.Error(t, err)
}

func TestParseToken_TamperedSignature(t *testing.T) {
	token, err := GenerateToken("john", "user-123", "access", time.Hour)
	require.NoError(t, err)

	_, err = ParseToken(token + "tampered")
	assert.Error(t, err)
}

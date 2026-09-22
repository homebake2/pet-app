package models

type User struct {
	Login    string `json:"login"`
	Password string `json:"password"`
}

type AuthResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	PetsCount    int    `json:"pets_count"`
}

// RefreshResponse — ответ POST /auth/refresh. В отличие от AuthResponse
// (используется login/register/guest) не содержит pets_count: клиент,
// обновляющий токены, уже знает количество питомцев из предыдущего логина.
type RefreshResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

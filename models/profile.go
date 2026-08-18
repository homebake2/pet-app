package models

type Profile struct {
	UserID     string  `json:"user_id"`
	FirstName  string  `json:"first_name"`
	MiddleName *string `json:"middle_name,omitempty"`
	LastName   *string `json:"last_name,omitempty"`
	Email      *string `json:"email,omitempty"`
	Phone      *string `json:"phone,omitempty"`
}

type ProfileResponse struct {
	ID         string `json:"id"`
	FirstName  string `json:"first_name"`
	MiddleName string `json:"middle_name,omitempty"`
	LastName   string `json:"last_name,omitempty"`
	Email      string `json:"email,omitempty"`
	Phone      string `json:"phone,omitempty"`
	Login      string `json:"login"`
}

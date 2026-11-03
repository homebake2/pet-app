package models

type User struct {
	Login    string `json:"login"`
	Password string `json:"password"`
}

type AuthResponse struct {
	AccessToken     string `json:"access_token"`
	RefreshToken    string `json:"refresh_token"`
	PetsCount       int    `json:"pets_count"`
	ConsentRequired bool   `json:"consent_required"`
}

// RefreshResponse — ответ POST /auth/refresh. В отличие от AuthResponse
// (используется login/register/guest) не содержит pets_count: клиент,
// обновляющий токены, уже знает количество питомцев из предыдущего логина.
type RefreshResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// ConsentDocumentText — элемент ответа GET /consent-documents.
type ConsentDocumentText struct {
	DocType  string `json:"doc_type"`
	Version  int    `json:"version"`
	Language string `json:"language"`
	Text     string `json:"text"`
}

// ConsentDocumentsResponse — ответ GET /consent-documents.
type ConsentDocumentsResponse struct {
	Documents []ConsentDocumentText `json:"documents"`
}

// ConsentDocumentStatus — элемент ответа GET /consents.
type ConsentDocumentStatus struct {
	DocType  string `json:"doc_type"`
	Version  int    `json:"version"`
	Accepted bool   `json:"accepted"`
}

// ConsentsResponse — ответ GET /consents.
type ConsentsResponse struct {
	ConsentRequired bool                    `json:"consent_required"`
	Documents       []ConsentDocumentStatus `json:"documents"`
}

// PostConsentsRequest — тело POST /consents.
type PostConsentsRequest struct {
	Documents []ConsentAcceptance `json:"documents"`
}

// ConsentAcceptance — принимаемая версия документа.
type ConsentAcceptance struct {
	DocType string `json:"doc_type"`
	Version int    `json:"version"`
}

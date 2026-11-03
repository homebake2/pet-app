package handlers

import (
	"encoding/json"
	"log"
	"myauthservice/database"
	"myauthservice/models"
	"myauthservice/openapi"
	"net/http"
)

// Документы, принятие которых запрашивается. Набор фиксирован: добавление
// документа — изменение кода и миграции с его текущей версией.
const (
	consentDocTerms        = "terms"
	consentDocPersonalData = "personal_data"
)

// isConsentRequired сообщает, что пользователь не принял текущие версии
// документов.
func isConsentRequired(userID string) (bool, error) {
	_, accepted, err := database.GetAuthState(userID)
	if err != nil {
		return false, err
	}
	return !accepted, nil
}

// ConsentDocumentsHandler обрабатывает GET /consent-documents — актуальные
// версии и тексты документов на запрошенном языке. Без авторизации: тексты
// нужны и на экране входа.
func ConsentDocumentsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
		return
	}

	language := r.URL.Query().Get("language")
	if language == "" {
		language = "ru"
	}
	if language != "ru" && language != "en" {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "language должен быть ru или en")
		return
	}

	docs, err := database.GetConsentDocumentTexts(language)
	if err != nil {
		log.Printf("DB get consent document texts error: %v", err)
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка базы данных")
		return
	}

	items := make([]models.ConsentDocumentText, 0, len(docs))
	for _, doc := range docs {
		items = append(items, models.ConsentDocumentText{
			DocType:  doc.DocType,
			Version:  doc.Version,
			Language: language,
			Text:     doc.Text,
		})
	}
	writeJSON(w, http.StatusOK, models.ConsentDocumentsResponse{Documents: items})
}

// ConsentsHandler обрабатывает /consents: GET — статус документов,
// POST — принятие. Оба метода доступны до принятия документов.
func ConsentsHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		getConsents(w, r)
	case http.MethodPost:
		postConsents(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
	}
}

func getConsents(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserIDWithoutConsent(w, r)
	if !ok {
		return
	}

	docs, err := database.GetConsentDocuments(userID)
	if err != nil {
		log.Printf("DB get consents error: %v", err)
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка базы данных")
		return
	}

	required := false
	items := make([]models.ConsentDocumentStatus, 0, len(docs))
	for _, doc := range docs {
		if !doc.Accepted {
			required = true
		}
		items = append(items, models.ConsentDocumentStatus{
			DocType:  doc.DocType,
			Version:  doc.Version,
			Accepted: doc.Accepted,
		})
	}

	writeJSON(w, http.StatusOK, models.ConsentsResponse{ConsentRequired: required, Documents: items})
}

func postConsents(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserIDWithoutConsent(w, r)
	if !ok {
		return
	}

	var req models.PostConsentsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректное тело запроса")
		return
	}

	// Ровно по одному разу каждый из двух документов; версия — положительное
	// число. Совпадение версии с текущей проверяется при записи (409).
	seen := map[string]bool{}
	accepted := make([]database.ConsentAcceptance, 0, len(req.Documents))
	for _, doc := range req.Documents {
		if doc.DocType != consentDocTerms && doc.DocType != consentDocPersonalData {
			writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Неизвестный doc_type")
			return
		}
		if seen[doc.DocType] {
			writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "doc_type передан более одного раза")
			return
		}
		if doc.Version < 1 {
			writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "version должна быть положительной")
			return
		}
		seen[doc.DocType] = true
		accepted = append(accepted, database.ConsentAcceptance{DocType: doc.DocType, Version: doc.Version})
	}
	if len(seen) != 2 {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Нужно принять оба документа")
		return
	}

	matched, err := database.AcceptConsents(userID, accepted)
	if err != nil {
		log.Printf("DB accept consents error: %v", err)
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка базы данных")
		return
	}
	if !matched {
		writeError(w, http.StatusConflict, openapi.CONSENTVERSIONMISMATCH, "Версия документа устарела, обновите приложение")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

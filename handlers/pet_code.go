package handlers

import (
	"net/http"
)

// PetCodePreviewHandler обрабатывает POST /pet-codes/preview — общий
// предпросмотр кода передачи питомца и кода приглашения к доступу. Тип кода
// определяется по его первому символу (см. petCodePrefixTransfer и
// petCodePrefixAccess); дальше работают правила соответствующего типа.
func PetCodePreviewHandler(w http.ResponseWriter, r *http.Request) {
	userID, code, ok := readCodeRequest(w, r)
	if !ok {
		return
	}

	switch code[0] {
	case petCodePrefixTransfer:
		previewPetTransfer(w, r, userID, code)
	case petCodePrefixAccess:
		previewPetAccessInvite(w, r, userID, code)
	}
}

package eventreg

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateValue_Valid(t *testing.T) {
	cases := []struct {
		eventType string
		value     string
	}{
		{"weight", `{"amount":4.5}`},
		{"weight", `{"amount":0.001}`},
		{"weight", `{"amount":400}`},
		{"temperature", `{"amount":0,"kind":"environment"}`},
		{"temperature", `{"amount":50,"kind":"body"}`},
		{"feeding", `{"amount":0.01,"unit":"g","food":"dry"}`},
		{"feeding", `{"amount":5000,"unit":"piece","food":"live_prey"}`},
		{"water", `{"amount":0.1}`},
		{"activity", `{"duration_min":1,"kind":"free_range"}`},
		{"activity", `{"duration_min":1440,"kind":"swim","distance_m":0}`},
		{"sleep", `{"duration_min":480}`},
		{"medication", `{"name":"Ципровет"}`},
		{"medication", `{"name":"Ципровет","dose_amount":0.001,"dose_unit":"mcg"}`},
		{"hygiene", `{"procedure":"water_change"}`},
		{"mood", `{"state":"hiding"}`},
		{"urine", `{"status":"normal"}`},
		{"defecation", `{"status":"abnormal"}`},
		{"vomit", `{"status":"normal"}`},
		{"diarrhea", `{"status":"abnormal"}`},
		{"other", `{"label":"хромота"}`},
		// null в необязательном поле трактуется как отсутствие поля.
		{"activity", `{"duration_min":30,"kind":"walk","distance_m":null}`},
	}

	for _, c := range cases {
		if msg := ValidateValue(c.eventType, json.RawMessage(c.value)); msg != "" {
			t.Errorf("%s %s: неожиданная ошибка %q", c.eventType, c.value, msg)
		}
	}
}

func TestValidateValue_Invalid(t *testing.T) {
	cases := []struct {
		name      string
		eventType string
		value     string
	}{
		{"неизвестный тип", "food", `{"amount":1}`},
		{"пустое значение", "weight", ``},
		{"не объект", "weight", `"4.5"`},
		{"массив", "weight", `[1]`},
		{"null", "weight", `null`},
		{"нет обязательного поля", "weight", `{}`},
		{"лишнее поле", "weight", `{"amount":5,"label":"x"}`},
		{"поле не того типа", "weight", `{"amount":"5"}`},
		{"ниже диапазона", "weight", `{"amount":0}`},
		{"выше диапазона", "weight", `{"amount":400.1}`},
		{"temperature без kind", "temperature", `{"amount":38}`},
		{"temperature с чужим kind", "temperature", `{"amount":38,"kind":"walk"}`},
		{"feeding без unit", "feeding", `{"amount":100,"food":"dry"}`},
		{"feeding с чужим food", "feeding", `{"amount":100,"unit":"g","food":"pizza"}`},
		{"water вне диапазона", "water", `{"amount":5000.1}`},
		{"activity distance вне диапазона", "activity", `{"duration_min":30,"kind":"walk","distance_m":100001}`},
		{"sleep вне диапазона", "sleep", `{"duration_min":1441}`},
		{"medication доза без единицы", "medication", `{"name":"Ципровет","dose_amount":1}`},
		{"medication единица без дозы", "medication", `{"name":"Ципровет","dose_unit":"mg"}`},
		{"medication пустое имя", "medication", `{"name":"   "}`},
		{"medication длинное имя", "medication", `{"name":"` + strings.Repeat("x", 101) + `"}`},
		{"hygiene с чужой процедурой", "hygiene", `{"procedure":"calm"}`},
		{"mood с чужим состоянием", "mood", `{"state":"bath"}`},
		{"status вне словаря", "urine", `{"status":"много"}`},
		{"other пустой label", "other", `{"label":""}`},
		{"other длинный label", "other", `{"label":"` + strings.Repeat("x", 51) + `"}`},
		{"обязательное поле как null", "weight", `{"amount":null}`},
	}

	for _, c := range cases {
		if msg := ValidateValue(c.eventType, json.RawMessage(c.value)); msg == "" {
			t.Errorf("%s: ожидалась ошибка валидации", c.name)
		}
	}
}

// Сообщение об ошибке обязано называть проблемное поле — иначе клиент не
// сможет показать пользователю, что именно исправить.
func TestValidateValue_MessageNamesField(t *testing.T) {
	msg := ValidateValue("weight", json.RawMessage(`{"amount":5,"unit":"g"}`))
	if !strings.Contains(msg, "unit") {
		t.Fatalf("сообщение об ошибке не называет лишнее поле: %q", msg)
	}
}

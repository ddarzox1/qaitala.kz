package kaspi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
)

const secret = "whsec_test_123"

func sign(body []byte, key string) string {
	m := hmac.New(sha256.New, []byte(key))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

// Денежный путь: подпись решает, запускать ли платную генерацию.
func TestVerifySignature(t *testing.T) {
	body := []byte(`{"event":"payment.success","paymentId":"42","amount":2990}`)

	if !VerifySignature(secret, body, sign(body, secret)) {
		t.Fatal("валидная подпись отклонена")
	}

	bad := []struct {
		name string
		body []byte
		hdr  string
	}{
		{"чужой секрет", body, sign(body, "whsec_attacker")},
		{"подменённое тело", []byte(`{"event":"payment.success","amount":1}`), sign(body, secret)},
		{"нет заголовка", body, ""},
		{"без префикса sha256=", body, hex.EncodeToString([]byte("x"))},
		{"не hex", body, "sha256=нехекс"},
		{"пустой секрет", body, sign(body, "")},
	}
	for _, c := range bad {
		key := secret
		if c.name == "пустой секрет" {
			key = ""
		}
		if VerifySignature(key, c.body, c.hdr) {
			t.Errorf("%s: подпись принята, хотя не должна", c.name)
		}
	}
}

// От срока жизни QR зависит, отдадим ли старый QR повторно.
func TestParseExpire(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	if got := parseExpire("2025-01-01T12:05:00", now); !got.Equal(time.Date(2025, 1, 1, 7, 5, 0, 0, time.UTC)) {
		t.Errorf("без пояса должно читаться как +05:00, получено %v", got)
	}
	if got := parseExpire("2025-01-01T12:05:00Z", now); !got.Equal(time.Date(2025, 1, 1, 12, 5, 0, 0, time.UTC)) {
		t.Errorf("RFC3339 разобран неверно: %v", got)
	}
	if got := parseExpire("мусор", now); !got.Equal(now.Add(5 * time.Minute)) {
		t.Errorf("фолбэк должен быть now+5м, получено %v", got)
	}
}

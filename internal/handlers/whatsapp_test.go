package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"opk-whatsapp-ai/internal/agent"
	"opk-whatsapp-ai/internal/users"
)

type fakeMessenger struct {
	mu   sync.Mutex
	sent map[string][]string
	read []string
}

func (f *fakeMessenger) SendText(_ context.Context, to, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sent == nil {
		f.sent = map[string][]string{}
	}
	f.sent[to] = append(f.sent[to], body)
	return nil
}

func (f *fakeMessenger) MarkRead(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.read = append(f.read, id)
	return nil
}

type fakeUsers map[string]*users.User

func (f fakeUsers) FindByPhone(_ context.Context, number string) (*users.User, error) {
	return f[number], nil
}

type fakeAgent struct {
	mu      sync.Mutex
	callers []agent.Caller
}

func (f *fakeAgent) RunForCaller(_ context.Context, msg string, c agent.Caller) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.callers = append(f.callers, c)
	return "answer to: " + msg, nil
}

const testSecret = "app-secret"

func sign(body string) string {
	mac := hmac.New(sha256.New, []byte(testSecret))
	mac.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func textWebhook(from, id, text string) string {
	return `{"object":"whatsapp_business_account","entry":[{"id":"1","changes":[{"field":"messages","value":{
		"messaging_product":"whatsapp","metadata":{"phone_number_id":"pn"},
		"messages":[{"from":"` + from + `","id":"` + id + `","timestamp":"1","type":"text","text":{"body":"` + text + `"}}]}}]}]}`
}

func setup() (*gin.Engine, *WhatsAppHandler, *fakeMessenger, *fakeAgent) {
	gin.SetMode(gin.TestMode)
	m := &fakeMessenger{}
	a := &fakeAgent{}
	u := fakeUsers{"233241234567": {ID: "u1", Name: "Ama", Role: "sales_manager"}}
	h := NewWhatsAppHandler(m, u, a, "verify-me", testSecret)
	r := gin.New()
	h.RegisterRoutes(r)
	return r, h, m, a
}

func post(r *gin.Engine, body, signature string) int {
	req := httptest.NewRequest(http.MethodPost, "/webhook/whatsapp", strings.NewReader(body))
	if signature != "" {
		req.Header.Set("X-Hub-Signature-256", signature)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec.Code
}

func TestWhatsAppVerify(t *testing.T) {
	r, _, _, _ := setup()

	for _, tc := range []struct {
		query string
		code  int
		body  string
	}{
		{"hub.mode=subscribe&hub.verify_token=verify-me&hub.challenge=123", http.StatusOK, "123"},
		{"hub.mode=subscribe&hub.verify_token=wrong&hub.challenge=123", http.StatusForbidden, ""},
		{"hub.mode=other&hub.verify_token=verify-me&hub.challenge=123", http.StatusForbidden, ""},
	} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/webhook/whatsapp?"+tc.query, nil))
		if rec.Code != tc.code || rec.Body.String() != tc.body {
			t.Errorf("%s: got %d %q", tc.query, rec.Code, rec.Body.String())
		}
	}
}

func TestWhatsAppRejectsBadSignature(t *testing.T) {
	r, h, m, _ := setup()
	body := textWebhook("233241234567", "wamid.1", "hello")

	if code := post(r, body, ""); code != http.StatusUnauthorized {
		t.Errorf("missing signature: %d", code)
	}
	if code := post(r, body, sign(body+"tampered")); code != http.StatusUnauthorized {
		t.Errorf("wrong signature: %d", code)
	}
	h.Wait()
	if len(m.sent) != 0 {
		t.Error("should not reply to unsigned webhooks")
	}
}

func TestWhatsAppRegisteredUserGetsAnswer(t *testing.T) {
	r, h, m, a := setup()
	body := textWebhook("233241234567", "wamid.2", "total sales today?")

	if code := post(r, body, sign(body)); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	// Duplicate delivery must be ignored.
	post(r, body, sign(body))
	h.Wait()

	got := m.sent["233241234567"]
	if len(got) != 1 || got[0] != "answer to: total sales today?" {
		t.Errorf("sent = %v", got)
	}
	if len(a.callers) != 1 || a.callers[0].Name != "Ama" || a.callers[0].Channel != "whatsapp" {
		t.Errorf("callers = %+v", a.callers)
	}
	if len(m.read) != 1 {
		t.Errorf("read receipts = %v", m.read)
	}
}

func TestWhatsAppUnregisteredSenderDenied(t *testing.T) {
	r, h, m, a := setup()
	body := textWebhook("233200000000", "wamid.3", "show me all customers")

	post(r, body, sign(body))
	h.Wait()

	if got := m.sent["233200000000"]; len(got) != 1 || got[0] != msgNotRegistered {
		t.Errorf("sent = %v", got)
	}
	if len(a.callers) != 0 {
		t.Error("agent must not run for unregistered senders")
	}
}

package whatsapp

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidSignature(t *testing.T) {
	body := []byte(`{"a":1}`)
	mac := hmac.New(sha256.New, []byte("s"))
	mac.Write(body)
	good := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if !ValidSignature(body, good, []byte("s")) {
		t.Error("valid signature rejected")
	}
	for _, bad := range []string{"", "sha256=zz", "sha1=" + good[7:], good[:len(good)-1] + "0"} {
		if ValidSignature(body, bad, []byte("s")) {
			t.Errorf("accepted %q", bad)
		}
	}
	if ValidSignature(body, good, nil) {
		t.Error("accepted with empty secret")
	}
}

func TestSplitMessage(t *testing.T) {
	text := strings.Repeat("word ", 50) + "\n\n" + strings.Repeat("more ", 50)
	chunks := splitMessage(text, 300)
	if len(chunks) != 2 || !strings.HasPrefix(chunks[1], "more") {
		t.Errorf("chunks = %q", chunks)
	}
	for _, c := range chunks {
		if len([]rune(c)) > 300 {
			t.Errorf("chunk too long: %d", len(c))
		}
	}
	if got := splitMessage("short", 300); len(got) != 1 || got[0] != "short" {
		t.Errorf("short = %q", got)
	}
}

func TestSendText(t *testing.T) {
	var got map[string]any
	var path, auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"messages":[{"id":"wamid.x"}]}`))
	}))
	defer srv.Close()

	c := NewClient("tok", "12345", "v24.0")
	c.BaseURL = srv.URL
	if err := c.SendText(context.Background(), "233241234567", "hi"); err != nil {
		t.Fatal(err)
	}
	if path != "/12345/messages" || auth != "Bearer tok" {
		t.Errorf("path=%s auth=%s", path, auth)
	}
	if got["to"] != "233241234567" || got["text"].(map[string]any)["body"] != "hi" {
		t.Errorf("payload = %v", got)
	}
}

package whatsapp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// WebhookPayload is the body Meta posts to the webhook.
type WebhookPayload struct {
	Object string  `json:"object"`
	Entry  []Entry `json:"entry"`
}

type Entry struct {
	ID      string   `json:"id"`
	Changes []Change `json:"changes"`
}

type Change struct {
	Field string      `json:"field"`
	Value ChangeValue `json:"value"`
}

type ChangeValue struct {
	MessagingProduct string    `json:"messaging_product"`
	Metadata         Metadata  `json:"metadata"`
	Contacts         []Contact `json:"contacts"`
	Messages         []Message `json:"messages"`
}

type Metadata struct {
	DisplayPhoneNumber string `json:"display_phone_number"`
	PhoneNumberID      string `json:"phone_number_id"`
}

type Contact struct {
	WaID    string `json:"wa_id"`
	Profile struct {
		Name string `json:"name"`
	} `json:"profile"`
}

type Message struct {
	From      string `json:"from"`
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Text      *struct {
		Body string `json:"body"`
	} `json:"text,omitempty"`
}

// IncomingMessages returns every user message in the payload, skipping
// delivery/read status updates.
func (p *WebhookPayload) IncomingMessages() []Message {
	var msgs []Message
	for _, e := range p.Entry {
		for _, c := range e.Changes {
			if c.Field != "messages" {
				continue
			}
			msgs = append(msgs, c.Value.Messages...)
		}
	}
	return msgs
}

// ValidSignature checks Meta's X-Hub-Signature-256 header ("sha256=<hex>"),
// an HMAC-SHA256 of the raw request body keyed with the app secret.
func ValidSignature(body []byte, header string, appSecret []byte) bool {
	sig, ok := strings.CutPrefix(header, "sha256=")
	if !ok || len(appSecret) == 0 {
		return false
	}

	got, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, appSecret)
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}

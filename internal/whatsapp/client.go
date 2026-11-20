// Package whatsapp integrates with the Meta WhatsApp Cloud API.
package whatsapp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// maxMessageRunes keeps each message under WhatsApp's 4096-character limit.
const maxMessageRunes = 4000

// Client sends messages through the WhatsApp Cloud API.
type Client struct {
	// BaseURL is the Graph API base, e.g. https://graph.facebook.com/v24.0.
	BaseURL       string
	accessToken   string
	phoneNumberID string
	httpClient    *http.Client
}

func NewClient(accessToken, phoneNumberID, apiVersion string) *Client {
	return &Client{
		BaseURL:       "https://graph.facebook.com/" + apiVersion,
		accessToken:   accessToken,
		phoneNumberID: phoneNumberID,
		httpClient:    &http.Client{Timeout: 30 * time.Second},
	}
}

// SendText sends body to the given WhatsApp number (international digits),
// splitting it into several messages if it is too long.
func (c *Client) SendText(ctx context.Context, to, body string) error {
	for _, chunk := range splitMessage(body, maxMessageRunes) {
		err := c.post(ctx, map[string]any{
			"messaging_product": "whatsapp",
			"recipient_type":    "individual",
			"to":                to,
			"type":              "text",
			"text": map[string]any{
				"preview_url": false,
				"body":        chunk,
			},
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// MarkRead marks an incoming message as read (blue ticks).
func (c *Client) MarkRead(ctx context.Context, messageID string) error {
	return c.post(ctx, map[string]any{
		"messaging_product": "whatsapp",
		"status":            "read",
		"message_id":        messageID,
	})
}

func (c *Client) post(ctx context.Context, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode WhatsApp request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.BaseURL+"/"+c.phoneNumberID+"/messages", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create WhatsApp request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send WhatsApp request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("WhatsApp API returned HTTP %d: %s", resp.StatusCode, respBody)
	}
	return nil
}

// splitMessage splits text into chunks of at most limit runes, preferring
// paragraph, then line, then word boundaries.
func splitMessage(text string, limit int) []string {
	text = strings.TrimSpace(text)
	var chunks []string

	for {
		runes := []rune(text)
		if len(runes) <= limit {
			if text != "" {
				chunks = append(chunks, text)
			}
			return chunks
		}

		window := string(runes[:limit])
		cut := -1
		for _, sep := range []string{"\n\n", "\n", " "} {
			if i := strings.LastIndex(window, sep); i > len(window)/2 {
				cut = i
				break
			}
		}
		if cut == -1 {
			cut = len(window)
		}

		chunks = append(chunks, strings.TrimSpace(text[:cut]))
		text = strings.TrimSpace(text[cut:])
	}
}

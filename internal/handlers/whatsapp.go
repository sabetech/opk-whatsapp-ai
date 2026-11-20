package handlers

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"opk-whatsapp-ai/internal/agent"
	"opk-whatsapp-ai/internal/phone"
	"opk-whatsapp-ai/internal/users"
	"opk-whatsapp-ai/internal/whatsapp"
)

const (
	maxWebhookBody       = 1 << 20
	maxWhatsAppMessage   = 4000
	whatsAppReplyTimeout = 3 * time.Minute
	maxConcurrentReplies = 4
)

// Replies sent to users.
const (
	msgNotRegistered = "Sorry, this WhatsApp number is not registered to use this service. Please contact your administrator."
	msgTextOnly      = "Sorry, I can only read text messages at the moment."
	msgTooLong       = "Sorry, that message is too long. Please keep questions under 4,000 characters."
	msgFailed        = "Sorry, I couldn't process your request right now. Please try again shortly."
)

// Messenger sends WhatsApp messages.
type Messenger interface {
	SendText(ctx context.Context, to, body string) error
	MarkRead(ctx context.Context, messageID string) error
}

// UserFinder resolves a phone number to a registered user.
type UserFinder interface {
	FindByPhone(ctx context.Context, number string) (*users.User, error)
}

// Responder answers a question on behalf of a caller.
type Responder interface {
	RunForCaller(ctx context.Context, message string, caller agent.Caller) (string, error)
}

// WhatsAppHandler receives WhatsApp Cloud API webhooks and replies to
// messages from registered users.
type WhatsAppHandler struct {
	messenger   Messenger
	users       UserFinder
	agent       Responder
	verifyToken string
	appSecret   []byte

	seen *seenSet
	sem  chan struct{}
	wg   sync.WaitGroup
}

func NewWhatsAppHandler(messenger Messenger, users UserFinder, agent Responder, verifyToken, appSecret string) *WhatsAppHandler {
	return &WhatsAppHandler{
		messenger:   messenger,
		users:       users,
		agent:       agent,
		verifyToken: verifyToken,
		appSecret:   []byte(appSecret),
		seen:        newSeenSet(time.Hour),
		sem:         make(chan struct{}, maxConcurrentReplies),
	}
}

func (h *WhatsAppHandler) RegisterRoutes(router *gin.Engine) {
	router.GET("/webhook/whatsapp", h.Verify)
	router.POST("/webhook/whatsapp", h.Receive)
}

// Verify answers Meta's one-time webhook verification challenge.
func (h *WhatsAppHandler) Verify(c *gin.Context) {
	mode := c.Query("hub.mode")
	token := c.Query("hub.verify_token")

	if mode != "subscribe" || subtle.ConstantTimeCompare([]byte(token), []byte(h.verifyToken)) != 1 {
		c.Status(http.StatusForbidden)
		return
	}

	c.String(http.StatusOK, c.Query("hub.challenge"))
}

// Receive validates the webhook signature, acknowledges immediately, and
// processes each new message in the background.
func (h *WhatsAppHandler) Receive(c *gin.Context) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxWebhookBody))
	if err != nil {
		c.Status(http.StatusBadRequest)
		return
	}

	if !whatsapp.ValidSignature(body, c.GetHeader("X-Hub-Signature-256"), h.appSecret) {
		log.Printf("whatsapp: rejected webhook with invalid signature")
		c.Status(http.StatusUnauthorized)
		return
	}

	var payload whatsapp.WebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		c.Status(http.StatusBadRequest)
		return
	}

	for _, msg := range payload.IncomingMessages() {
		// Meta retries deliveries; process each message ID once.
		if msg.ID == "" || !h.seen.add(msg.ID) {
			continue
		}

		h.wg.Add(1)
		go func(msg whatsapp.Message) {
			defer h.wg.Done()
			h.sem <- struct{}{}
			defer func() { <-h.sem }()
			h.handleMessage(msg)
		}(msg)
	}

	c.Status(http.StatusOK)
}

// Wait blocks until in-flight messages finish (used in tests and shutdown).
func (h *WhatsAppHandler) Wait() { h.wg.Wait() }

func (h *WhatsAppHandler) handleMessage(msg whatsapp.Message) {
	ctx, cancel := context.WithTimeout(context.Background(), whatsAppReplyTimeout)
	defer cancel()

	from := msg.From
	masked := phone.Mask(from)

	user, err := h.users.FindByPhone(ctx, from)
	switch {
	case errors.Is(err, users.ErrAmbiguous):
		log.Printf("whatsapp: %s matches several users; denying", masked)
		h.reply(ctx, from, msgNotRegistered)
		return
	case err != nil:
		log.Printf("whatsapp: user lookup for %s failed: %v", masked, err)
		h.reply(ctx, from, msgFailed)
		return
	case user == nil:
		log.Printf("whatsapp: unregistered sender %s", masked)
		h.reply(ctx, from, msgNotRegistered)
		return
	}

	if err := h.messenger.MarkRead(ctx, msg.ID); err != nil {
		log.Printf("whatsapp: mark read failed: %v", err)
	}

	if msg.Type != "text" || msg.Text == nil {
		h.reply(ctx, from, msgTextOnly)
		return
	}

	text := strings.TrimSpace(msg.Text.Body)
	if text == "" {
		return
	}
	if len(text) > maxWhatsAppMessage {
		h.reply(ctx, from, msgTooLong)
		return
	}

	log.Printf("whatsapp: message %s from user %s (%s)", msg.ID, user.ID, user.Role)

	answer, err := h.agent.RunForCaller(ctx, text, agent.Caller{
		Name:    user.Name,
		Role:    user.Role,
		Channel: "whatsapp",
	})
	if err != nil {
		log.Printf("whatsapp: agent failed for message %s: %v", msg.ID, err)
		h.reply(ctx, from, msgFailed)
		return
	}

	h.reply(ctx, from, answer)
}

func (h *WhatsAppHandler) reply(ctx context.Context, to, body string) {
	if err := h.messenger.SendText(ctx, to, body); err != nil {
		log.Printf("whatsapp: send to %s failed: %v", phone.Mask(to), err)
	}
}

// seenSet remembers message IDs for a while to drop duplicate deliveries.
type seenSet struct {
	mu  sync.Mutex
	ttl time.Duration
	ids map[string]time.Time
}

func newSeenSet(ttl time.Duration) *seenSet {
	return &seenSet{ttl: ttl, ids: map[string]time.Time{}}
}

// add records id and reports whether it was new.
func (s *seenSet) add(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	if t, ok := s.ids[id]; ok && now.Sub(t) < s.ttl {
		return false
	}

	if len(s.ids) > 1000 {
		for k, t := range s.ids {
			if now.Sub(t) >= s.ttl {
				delete(s.ids, k)
			}
		}
	}

	s.ids[id] = now
	return true
}

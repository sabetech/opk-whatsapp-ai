package handlers

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"opk-whatsapp-ai/internal/agent"
)

type ChatHandler struct {
	agent *agent.Agent
}

func NewChatHandler(aiAgent *agent.Agent) *ChatHandler {
	return &ChatHandler{
		agent: aiAgent,
	}
}

type ChatRequest struct {
	Message string `json:"message" binding:"required"`
}

type ChatResponse struct {
	Reply string `json:"reply"`
}

// RegisterRoutes mounts the chat endpoint behind the given middleware
// (e.g. RequireAPIKey).
func (h *ChatHandler) RegisterRoutes(router *gin.Engine, middleware ...gin.HandlerFunc) {
	router.POST("/api/chat", append(middleware, h.Chat)...)
}

func (h *ChatHandler) Chat(c *gin.Context) {
	var req ChatRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "A valid message is required",
		})
		return
	}

	message := strings.TrimSpace(req.Message)

	if message == "" || len(message) > 4000 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Message must contain between 1 and 4000 characters",
		})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 120*time.Second)
	defer cancel()

	reply, err := h.agent.Run(ctx, message)
	if err != nil {
		log.Printf("chat: agent run failed: %v", err)
		c.JSON(http.StatusBadGateway, gin.H{
			"error": "Unable to process your request",
		})
		return
	}

	c.JSON(http.StatusOK, ChatResponse{
		Reply: reply,
	})
}

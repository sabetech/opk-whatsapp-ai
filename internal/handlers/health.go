package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"opk-whatsapp-ai/internal/pocketbase"
)

type HealthHandler struct {
	pocketBase *pocketbase.Client
}

func NewHealthHandler(pb *pocketbase.Client) *HealthHandler {
	return &HealthHandler{
		pocketBase: pb,
	}
}

func (h *HealthHandler) RegisterRoutes(router *gin.Engine) {
	router.GET("/health", h.Health)
	router.GET("/health/pocketbase", h.PocketBaseHealth)
}

func (h *HealthHandler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"service": "opk-whatsapp-ai",
	})
}

func (h *HealthHandler) PocketBaseHealth(c *gin.Context) {
	if err := h.pocketBase.Health(c.Request.Context()); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "unavailable",
			"error":  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"service": "pocketbase",
	})
}

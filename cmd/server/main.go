package main

import (
	"context"
	"log"
	"time"

	"github.com/gin-gonic/gin"

	"opk-whatsapp-ai/internal/agent"
	"opk-whatsapp-ai/internal/config"
	"opk-whatsapp-ai/internal/handlers"
	"opk-whatsapp-ai/internal/llm"
	"opk-whatsapp-ai/internal/pocketbase"
	"opk-whatsapp-ai/internal/tools"
	"opk-whatsapp-ai/internal/users"
	"opk-whatsapp-ai/internal/whatsapp"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Load configuration: %v", err)
	}

	// Initialize PocketBase.
	pbClient := pocketbase.NewClient(cfg.PocketBaseURL)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := pbClient.Authenticate(ctx, cfg.PocketBaseEmail, cfg.PocketBasePassword); err != nil {
		log.Fatalf("Failed to authenticate with PocketBase: %v", err)
	}

	log.Println("Successfully authenticated with PocketBase!")

	// Initialize LLM.
	llmClient := llm.NewClient(cfg.OpenRouterAPIKey, cfg.OpenRouterBaseURL, cfg.OpenRouterModel)

	// Register available tools.
	access := tools.NewCollectionAccess(cfg.AllowedCollections)
	env := tools.NewEnv(pbClient, access, cfg.Location)
	registry := tools.NewRegistry()

	for _, tool := range tools.All(env) {
		if err := registry.Register(tool); err != nil {
			log.Fatalf("Register tool: %v", err)
		}
	}

	// Initialize agent.
	aiAgent := agent.New(llmClient, registry, cfg.Location, cfg.Currency)

	// Initialize Gin.
	router := gin.Default()

	handlers.NewHealthHandler(pbClient).RegisterRoutes(router)
	handlers.NewChatHandler(aiAgent).RegisterRoutes(router, handlers.RequireAPIKey(cfg.ChatAPIKey))

	if cfg.WhatsAppEnabled {
		handlers.NewWhatsAppHandler(
			whatsapp.NewClient(cfg.WhatsAppAccessToken, cfg.WhatsAppPhoneNumberID, cfg.WhatsAppAPIVersion),
			users.NewDirectory(pbClient, cfg.DefaultCountryCode),
			aiAgent,
			cfg.WhatsAppVerifyToken,
			cfg.WhatsAppAppSecret,
		).RegisterRoutes(router)
		log.Println("WhatsApp webhook enabled at /webhook/whatsapp")
	} else {
		log.Println("WhatsApp not configured; webhook disabled")
	}

	log.Printf("%s server starting on port %s", cfg.AgentName, cfg.Port)

	if err := router.Run(":" + cfg.Port); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

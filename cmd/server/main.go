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
	registry := tools.NewRegistry()

	for _, tool := range []tools.Tool{
		tools.NewPocketBaseHealthTool(pbClient),
		tools.NewListCollectionsTool(pbClient, access),
		tools.NewCountTool(pbClient, access),
		tools.NewSearchTool(pbClient, access),
		tools.NewRetrieveTool(pbClient, access),
	} {
		if err := registry.Register(tool); err != nil {
			log.Fatalf("Register tool: %v", err)
		}
	}

	// Initialize agent.
	aiAgent := agent.New(llmClient, registry)

	// Initialize Gin.
	router := gin.Default()

	handlers.NewHealthHandler(pbClient).RegisterRoutes(router)
	handlers.NewChatHandler(aiAgent).RegisterRoutes(router, handlers.RequireAPIKey(cfg.ChatAPIKey))

	log.Printf("%s server starting on port %s", cfg.AgentName, cfg.Port)

	if err := router.Run(":" + cfg.Port); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

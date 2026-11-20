package config

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	Port string
	// ChatAPIKey must be sent as "Authorization: Bearer <key>" to /api/chat.
	ChatAPIKey string

	PocketBaseURL      string
	PocketBaseEmail    string
	PocketBasePassword string
	// AllowedCollections restricts which collections the agent may read.
	// Empty means every non-system collection (names not starting with "_").
	AllowedCollections []string

	OpenRouterAPIKey  string
	OpenRouterBaseURL string
	OpenRouterModel   string

	AgentName     string
	AgentLogLevel string
}

// Load reads configuration from the environment (and .env if present) and
// validates that required values are set.
func Load() (*Config, error) {
	_ = godotenv.Load()

	cfg := &Config{
		Port:               getEnv("PORT", "8085"),
		ChatAPIKey:         getEnv("CHAT_API_KEY", ""),
		PocketBaseURL:      getEnv("POCKETBASE_URL", ""),
		PocketBaseEmail:    getEnv("POCKETBASE_EMAIL", ""),
		PocketBasePassword: getEnv("POCKETBASE_PASSWORD", ""),
		AllowedCollections: splitList(getEnv("POCKETBASE_ALLOWED_COLLECTIONS", "")),
		OpenRouterAPIKey:   getEnv("OPENROUTER_API_KEY", ""),
		OpenRouterBaseURL:  getEnv("OPENROUTER_BASE_URL", "https://openrouter.ai/api/v1"),
		OpenRouterModel:    getEnv("OPENROUTER_MODEL", ""),
		AgentName:          getEnv("AGENT_NAME", "opk-whatsapp-ai"),
		AgentLogLevel:      getEnv("AGENT_LOG_LEVEL", "info"),
	}

	required := map[string]string{
		"CHAT_API_KEY":        cfg.ChatAPIKey,
		"POCKETBASE_URL":      cfg.PocketBaseURL,
		"POCKETBASE_EMAIL":    cfg.PocketBaseEmail,
		"POCKETBASE_PASSWORD": cfg.PocketBasePassword,
		"OPENROUTER_API_KEY":  cfg.OpenRouterAPIKey,
		"OPENROUTER_MODEL":    cfg.OpenRouterModel,
	}

	var missing []string
	for key, value := range required {
		if value == "" {
			missing = append(missing, key)
		}
	}

	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("missing required configuration: %s", strings.Join(missing, ", "))
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func splitList(value string) []string {
	var items []string
	for item := range strings.SplitSeq(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items
}

package tools

import (
	"context"
	"encoding/json"

	"opk-whatsapp-ai/internal/llm"
	"opk-whatsapp-ai/internal/pocketbase"
)

func NewPocketBaseHealthTool(client *pocketbase.Client) Tool {
	return Tool{
		Definition: llm.ToolDefinition{
			Type: "function",
			Function: llm.FunctionSchema{
				Name:        "check_pocketbase_health",
				Description: "Checks whether the PocketBase application is reachable and healthy.",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {},
					"additionalProperties": false
				}`),
			},
		},
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			if err := client.Health(ctx); err != nil {
				return map[string]string{
					"status": "unavailable",
				}, err
			}

			return map[string]string{
				"status":  "healthy",
				"service": "PocketBase",
			}, nil
		},
	}
}

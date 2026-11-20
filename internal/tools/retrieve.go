package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"opk-whatsapp-ai/internal/llm"
	"opk-whatsapp-ai/internal/pocketbase"
)

// NewRetrieveTool fetches a single record by ID.
func NewRetrieveTool(client *pocketbase.Client, access *CollectionAccess) Tool {
	return Tool{
		Definition: llm.ToolDefinition{
			Type: "function",
			Function: llm.FunctionSchema{
				Name:        "get_record",
				Description: "Retrieves a single record by its ID from a collection.",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"collection": {
							"type": "string",
							"description": "Collection name, as returned by list_collections."
						},
						"id": {
							"type": "string",
							"description": "The record ID."
						},
						"expand": {
							"type": "string",
							"description": "Optional comma-separated relation fields to expand."
						}
					},
					"required": ["collection", "id"],
					"additionalProperties": false
				}`),
			},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				Collection string `json:"collection"`
				ID         string `json:"id"`
				Expand     string `json:"expand"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			if err := access.Check(args.Collection); err != nil {
				return nil, err
			}
			if args.ID == "" {
				return nil, fmt.Errorf("id is required")
			}

			return client.GetRecord(ctx, args.Collection, args.ID, args.Expand)
		},
	}
}

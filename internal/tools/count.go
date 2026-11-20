package tools

import (
	"context"
	"encoding/json"

	"opk-whatsapp-ai/internal/llm"
	"opk-whatsapp-ai/internal/pocketbase"
)

// NewCountTool counts records in a collection, optionally filtered.
func NewCountTool(client *pocketbase.Client, access *CollectionAccess) Tool {
	return Tool{
		Definition: llm.ToolDefinition{
			Type: "function",
			Function: llm.FunctionSchema{
				Name:        "count_records",
				Description: "Counts records in a collection, optionally matching a PocketBase filter expression.",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"collection": {
							"type": "string",
							"description": "Collection name, as returned by list_collections."
						},
						"filter": {
							"type": "string",
							"description": "Optional PocketBase filter, e.g. status = 'pending' && created >= '2026-01-01 00:00:00'."
						}
					},
					"required": ["collection"],
					"additionalProperties": false
				}`),
			},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				Collection string `json:"collection"`
				Filter     string `json:"filter"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			if err := access.Check(args.Collection); err != nil {
				return nil, err
			}

			count, err := client.CountRecords(ctx, args.Collection, args.Filter)
			if err != nil {
				return nil, err
			}

			return map[string]any{
				"collection": args.Collection,
				"filter":     args.Filter,
				"count":      count,
			}, nil
		},
	}
}

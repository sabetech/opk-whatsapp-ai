package tools

import (
	"context"
	"encoding/json"

	"opk-whatsapp-ai/internal/llm"
	"opk-whatsapp-ai/internal/pocketbase"
)

const (
	defaultSearchLimit = 20
	maxSearchLimit     = 50
)

// NewSearchTool lists records in a collection matching a filter.
func NewSearchTool(client *pocketbase.Client, access *CollectionAccess) Tool {
	return Tool{
		Definition: llm.ToolDefinition{
			Type: "function",
			Function: llm.FunctionSchema{
				Name:        "search_records",
				Description: "Searches records in a collection using an optional PocketBase filter and sort. Returns at most 50 records per call; use page to see more.",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"collection": {
							"type": "string",
							"description": "Collection name, as returned by list_collections."
						},
						"filter": {
							"type": "string",
							"description": "Optional PocketBase filter, e.g. name ~ 'Kofi' && balance > 0."
						},
						"sort": {
							"type": "string",
							"description": "Optional sort, e.g. -created (newest first) or name."
						},
						"fields": {
							"type": "string",
							"description": "Optional comma-separated list of fields to return, to keep results small."
						},
						"expand": {
							"type": "string",
							"description": "Optional comma-separated relation fields to expand."
						},
						"limit": {
							"type": "integer",
							"minimum": 1,
							"maximum": 50,
							"description": "Records per page (default 20)."
						},
						"page": {
							"type": "integer",
							"minimum": 1,
							"description": "Page number (default 1)."
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
				Sort       string `json:"sort"`
				Fields     string `json:"fields"`
				Expand     string `json:"expand"`
				Limit      int    `json:"limit"`
				Page       int    `json:"page"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			if err := access.Check(args.Collection); err != nil {
				return nil, err
			}

			if args.Limit <= 0 {
				args.Limit = defaultSearchLimit
			}
			if args.Limit > maxSearchLimit {
				args.Limit = maxSearchLimit
			}
			if args.Page <= 0 {
				args.Page = 1
			}

			result, err := client.ListRecords(ctx, args.Collection, pocketbase.ListOptions{
				Filter:  args.Filter,
				Sort:    args.Sort,
				Fields:  args.Fields,
				Expand:  args.Expand,
				Page:    args.Page,
				PerPage: args.Limit,
			})
			if err != nil {
				return nil, err
			}

			return map[string]any{
				"collection":  args.Collection,
				"page":        result.Page,
				"total_items": result.TotalItems,
				"total_pages": result.TotalPages,
				"items":       result.Items,
			}, nil
		},
	}
}

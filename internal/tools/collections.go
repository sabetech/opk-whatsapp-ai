package tools

import (
	"context"
	"encoding/json"

	"opk-whatsapp-ai/internal/llm"
	"opk-whatsapp-ai/internal/pocketbase"
)

// NewListCollectionsTool lets the model discover which collections exist and
// what fields they have, so it can build valid search/count queries.
func NewListCollectionsTool(client *pocketbase.Client, access *CollectionAccess) Tool {
	return Tool{
		Definition: llm.ToolDefinition{
			Type: "function",
			Function: llm.FunctionSchema{
				Name:        "list_collections",
				Description: "Lists the data collections available in the application, with each collection's field names and types. Call this first when unsure which collection or field holds the information you need.",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {},
					"additionalProperties": false
				}`),
			},
		},
		Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
			collections, err := client.ListCollections(ctx)
			if err != nil {
				return nil, err
			}

			type collectionInfo struct {
				Name   string            `json:"name"`
				Fields map[string]string `json:"fields"`
			}

			result := make([]collectionInfo, 0, len(collections))
			for _, col := range collections {
				if col.System || !access.Allowed(col.Name) {
					continue
				}

				fields := make(map[string]string, len(col.Fields))
				for _, f := range col.Fields {
					if f.Type == "password" || sensitiveFields[f.Name] {
						continue
					}
					fields[f.Name] = f.Type
				}

				result = append(result, collectionInfo{Name: col.Name, Fields: fields})
			}

			return map[string]any{"collections": result}, nil
		},
	}
}

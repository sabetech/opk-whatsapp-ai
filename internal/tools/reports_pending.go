package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"opk-whatsapp-ai/internal/pocketbase"
)

type pendingSection struct {
	name       string
	collection string
	filter     string
	sort       string
	expand     string
	project    func(env *Env, rec map[string]any) map[string]any
}

var pendingSections = []pendingSection{
	{
		name:       "orders",
		collection: "orders",
		filter:     "status = 'pending' && deleted_at = ''",
		sort:       "-date_time",
		expand:     "customer_id",
		project: func(env *Env, r map[string]any) map[string]any {
			return map[string]any{
				"id":           str(r, "id"),
				"order_number": getPath(r, "order_number"),
				"customer":     str(r, "customer_id.name"),
				"total_amount": num(r, "total_amount"),
				"date":         env.localDate(getPath(r, "date_time")),
			}
		},
	},
	{
		name:       "warehouse_orders",
		collection: "warehouse_orders",
		filter:     "status = 'pending'",
		expand:     "order_id,order_id.customer_id",
		project: func(env *Env, r map[string]any) map[string]any {
			return map[string]any{
				"id":           str(r, "id"),
				"order_number": getPath(r, "order_id.order_number"),
				"customer":     str(r, "order_id.customer_id.name"),
				"order_date":   env.localDate(getPath(r, "order_id.date_time")),
			}
		},
	},
	{
		name:       "loadouts",
		collection: "loadouts",
		filter:     "status = 'pending'",
		sort:       "-date",
		expand:     "vse_id",
		project: func(env *Env, r map[string]any) map[string]any {
			return map[string]any{
				"id":   str(r, "id"),
				"vse":  str(r, "vse_id.name"),
				"date": env.localDate(getPath(r, "date")),
			}
		},
	},
	{
		name:       "stock_adjustment_requests",
		collection: "stock_adjustment_requests",
		filter:     "status = 'pending'",
		sort:       "-date",
		project: func(env *Env, r map[string]any) map[string]any {
			return map[string]any{
				"id":           str(r, "id"),
				"reference":    str(r, "reference"),
				"direction":    str(r, "direction"),
				"reason":       str(r, "reason"),
				"requested_by": str(r, "requested_by"),
				"date":         env.localDate(getPath(r, "date")),
			}
		},
	},
	{
		name:       "vse_field_sales",
		collection: "vse_field_sales",
		filter:     "sale_status = 'pending'",
		sort:       "-date",
		expand:     "vse_customer_id",
		project: func(env *Env, r map[string]any) map[string]any {
			return map[string]any{
				"id":        str(r, "id"),
				"reference": str(r, "reference"),
				"vse":       str(r, "vse_customer_id.name"),
				"date":      env.localDate(getPath(r, "date")),
			}
		},
	},
	{
		name:       "vse_empties",
		collection: "vse_field_sales",
		filter:     "empties_status = 'pending' && empties_received > 0",
		sort:       "-date",
		expand:     "vse_customer_id",
		project: func(env *Env, r map[string]any) map[string]any {
			return map[string]any{
				"id":               str(r, "id"),
				"reference":        str(r, "reference"),
				"vse":              str(r, "vse_customer_id.name"),
				"empties_received": num(r, "empties_received"),
				"date":             env.localDate(getPath(r, "date")),
			}
		},
	},
}

// NewPendingApprovalsTool lists everything awaiting approval or action.
func NewPendingApprovalsTool(env *Env) Tool {
	return newTool(
		"pending_approvals",
		"Lists everything awaiting approval or action: pending orders, pending warehouse orders, pending loadouts, "+
			"pending stock adjustment requests, VSE field sales awaiting approval and VSE empties awaiting approval. "+
			"Returns a count per category and the most recent items.",
		`{
			"type": "object",
			"properties": {
				"items_per_category": {"type": "integer", "minimum": 0, "maximum": 50, "description": "How many recent items to list per category (default 10)."}
			},
			"additionalProperties": false
		}`,
		func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				ItemsPerCategory *int `json:"items_per_category"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}

			perCategory := 10
			if args.ItemsPerCategory != nil {
				perCategory = max(0, min(50, *args.ItemsPerCategory))
			}

			result := map[string]any{}
			total := 0

			for _, s := range pendingSections {
				if !env.Access.Allowed(s.collection) {
					continue
				}

				res, err := env.PB.ListRecords(ctx, s.collection, pocketbase.ListOptions{
					Filter:  s.filter,
					Sort:    s.sort,
					Expand:  s.expand,
					PerPage: max(1, perCategory),
				})
				if err != nil {
					return nil, err
				}

				items := make([]map[string]any, 0, len(res.Items))
				for i, r := range res.Items {
					if i >= perCategory {
						break
					}
					items = append(items, s.project(env, r))
				}

				total += res.TotalItems
				section := map[string]any{
					"count": res.TotalItems,
					"items": items,
				}
				if res.TotalItems > len(items) {
					section["note"] = fmt.Sprintf("only %d of %d items listed; do not describe the unlisted ones", len(items), res.TotalItems)
				}
				result[s.name] = section
			}

			result["total_pending"] = total
			return result, nil
		},
	)
}

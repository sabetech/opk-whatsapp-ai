package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"

	"opk-whatsapp-ai/internal/pocketbase"
)

// NewStockStatusTool reports current warehouse stock per product.
func NewStockStatusTool(env *Env) Tool {
	return newTool(
		"stock_status",
		"Current warehouse stock per product (quantity on hand, prices, stock value at wholesale price). "+
			"Optionally filter by product name/code, or list products below a low-stock threshold. Excludes deleted and empties-only products.",
		`{
			"type": "object",
			"properties": {
				"product": {"type": "string", "description": "Optional product ID, name, code name or product code (partial match)."},
				"low_stock_below": {"type": "number", "description": "Only include products with quantity below this value."},
				"limit": {"type": "integer", "minimum": 1, "maximum": 200, "description": "Maximum products to list (default 50)."}
			},
			"additionalProperties": false
		}`,
		func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				Product       string   `json:"product"`
				LowStockBelow *float64 `json:"low_stock_below"`
				Limit         int      `json:"limit"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			if err := env.requireCollections("warehouse_stock", "products"); err != nil {
				return nil, err
			}

			filter := and(
				"product_id.deleted_at = ''",
				"product_id.empties_only = false",
				productClause("product_id", args.Product),
			)
			if args.LowStockBelow != nil {
				filter = and(filter, fmt.Sprintf("quantity < %g", *args.LowStockBelow))
			}

			records, truncated, err := env.PB.ListAll(ctx, "warehouse_stock", pocketbase.ListOptions{
				Filter: filter,
				Expand: "product_id",
			}, maxScanRecords)
			if err != nil {
				return nil, err
			}

			items := make([]map[string]any, 0, len(records))
			var totalUnits, totalValue float64
			for _, r := range records {
				qty := num(r, "quantity")
				price := num(r, "product_id.wholesale_price")
				totalUnits += qty
				totalValue += qty * price
				items = append(items, map[string]any{
					"product":         str(r, "product_id.sku_name"),
					"code_name":       str(r, "product_id.code_name"),
					"product_code":    str(r, "product_id.product_code"),
					"quantity":        qty,
					"wholesale_price": price,
					"retail_price":    num(r, "product_id.retail_price"),
					"returnable":      getPath(r, "product_id.returnable"),
				})
			}

			sort.SliceStable(items, func(i, j int) bool {
				if args.LowStockBelow != nil {
					return items[i]["quantity"].(float64) < items[j]["quantity"].(float64)
				}
				return items[i]["product"].(string) < items[j]["product"].(string)
			})

			limit := clampLimit(args.Limit, 50, 200)
			result := map[string]any{
				"totals": map[string]any{
					"products":                len(items),
					"total_units":             round2(totalUnits),
					"stock_value_(wholesale)": round2(totalValue),
				},
				"products": items[:min(limit, len(items))],
			}
			if len(items) > limit {
				result["note"] = fmt.Sprintf("showing %d of %d products", limit, len(items))
			}
			if truncated {
				result["warning"] = "result set was truncated"
			}
			return result, nil
		},
	)
}

var inventoryLogTypes = []string{
	"supplier_receipt", "vse_loadout", "retail_sale", "wholesale_sale", "breakage", "promo_out",
	"promo_reimbursement", "opening_stock", "adjustment_increase", "adjustment_decrease", "customer_return", "vse_return",
}

// NewStockMovementsTool summarises inventory log movements.
func NewStockMovementsTool(env *Env) Tool {
	typesJSON, _ := json.Marshal(inventoryLogTypes)

	return newTool(
		"stock_movements",
		"Summarises inventory movements (inventory_logs) for a period: receipts, sales, loadouts, breakages, promos, adjustments and returns. "+
			"Totals quantity by movement type, product or date. Quantities are signed: negative = stock out (sales, loadouts, breakages), "+
			"positive = stock in (receipts, returns). Defaults to the last 30 days.",
		`{
			"type": "object",
			"properties": {`+periodParams+`,
				"product": {"type": "string", "description": "Optional product ID, name or code (partial match)."},
				"type": {"type": "string", "enum": `+string(typesJSON)+`, "description": "Optional movement type filter."},
				"group_by": {"type": "string", "enum": ["type", "product", "product_and_type", "day", "week", "month"], "description": "Breakdown (default type)."},
				"limit": {"type": "integer", "minimum": 1, "maximum": 200}
			},
			"additionalProperties": false
		}`,
		func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				From    string `json:"from"`
				To      string `json:"to"`
				Product string `json:"product"`
				Type    string `json:"type"`
				GroupBy string `json:"group_by"`
				Limit   int    `json:"limit"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			if err := env.requireCollections("inventory_logs"); err != nil {
				return nil, err
			}

			p, err := env.parsePeriod(args.From, args.To, 30)
			if err != nil {
				return nil, err
			}

			typeClause := ""
			if args.Type != "" {
				typeClause = "type = " + quote(args.Type)
			}

			var groupBy []string
			sortBy, asc := "", false
			switch args.GroupBy {
			case "", "type":
				groupBy = []string{"type"}
			case "product":
				groupBy = []string{"product_id.sku_name"}
			case "product_and_type":
				groupBy = []string{"product_id.sku_name", "type"}
			case "day", "week", "month":
				groupBy = []string{"date:" + args.GroupBy}
				sortBy, asc = "group", true
			default:
				return nil, fmt.Errorf("invalid group_by %q", args.GroupBy)
			}

			res, err := env.aggregate(ctx, aggQuery{
				Collection: "inventory_logs",
				Filter:     and(p.filter("date"), productClause("product_id", args.Product), typeClause),
				GroupBy:    groupBy,
				Metrics:    []metricSpec{{Op: "sum", Field: "quantity"}},
				SortBy:     sortBy,
				Asc:        asc,
				Limit:      args.Limit,
			})
			if err != nil {
				return nil, err
			}

			return map[string]any{
				"period":       p.describe(),
				"movements":    res.Totals["count"],
				"net_qty":      res.Totals["sum_quantity"],
				"note":         "quantities are signed: negative = stock out, positive = stock in",
				"breakdown":    res.Groups,
				"total_groups": res.TotalGroups,
			}, nil
		},
	)
}

// NewStockTakeVariancesTool reports variances from a stock take or empties count.
func NewStockTakeVariancesTool(env *Env) Tool {
	return newTool(
		"stock_take_variances",
		"Shows differences between system and physical counts from a stock take (kind=stock) or empties count (kind=empties). "+
			"Uses the most recent count unless take_id is given.",
		`{
			"type": "object",
			"properties": {
				"kind": {"type": "string", "enum": ["stock", "empties"], "description": "Which count to inspect (default stock)."},
				"take_id": {"type": "string", "description": "Optional specific count ID; defaults to the latest."},
				"include_zero": {"type": "boolean", "description": "Include items with no variance (default false)."}
			},
			"additionalProperties": false
		}`,
		func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				Kind        string `json:"kind"`
				TakeID      string `json:"take_id"`
				IncludeZero bool   `json:"include_zero"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}

			headers, items := "stock_takes", "stock_take_items"
			switch args.Kind {
			case "", "stock":
			case "empties":
				headers, items = "empties_count_takes", "empties_count_items"
			default:
				return nil, fmt.Errorf("invalid kind %q", args.Kind)
			}
			if err := env.requireCollections(headers, items); err != nil {
				return nil, err
			}

			var take map[string]any
			if args.TakeID != "" {
				if !isRecordID(args.TakeID) {
					return nil, fmt.Errorf("invalid take_id %q", args.TakeID)
				}
				rec, err := env.PB.GetRecord(ctx, headers, args.TakeID, "")
				if err != nil {
					return nil, err
				}
				take = rec
			} else {
				res, err := env.PB.ListRecords(ctx, headers, pocketbase.ListOptions{Sort: "-date", PerPage: 1})
				if err != nil {
					return nil, err
				}
				if len(res.Items) == 0 {
					return map[string]any{"found": false, "message": "No " + headers + " have been recorded yet."}, nil
				}
				take = res.Items[0]
			}

			filter := "take_id = " + quote(str(take, "id"))
			if !args.IncludeZero {
				filter = and(filter, "variance != 0")
			}

			records, _, err := env.PB.ListAll(ctx, items, pocketbase.ListOptions{Filter: filter, Expand: "product_id"}, maxScanRecords)
			if err != nil {
				return nil, err
			}

			rows := make([]map[string]any, 0, len(records))
			var net, abs float64
			shortages := 0
			for _, r := range records {
				v := num(r, "variance")
				net += v
				abs += math.Abs(v)
				if v < 0 {
					shortages++
				}
				rows = append(rows, map[string]any{
					"product":      str(r, "product_id.sku_name"),
					"system_qty":   num(r, "system_qty"),
					"physical_qty": num(r, "physical_qty"),
					"variance":     v,
				})
			}
			sort.SliceStable(rows, func(i, j int) bool {
				return math.Abs(rows[i]["variance"].(float64)) > math.Abs(rows[j]["variance"].(float64))
			})

			return map[string]any{
				"found": true,
				"take": map[string]any{
					"id":       str(take, "id"),
					"date":     env.localDate(getPath(take, "date")),
					"taken_by": str(take, "taken_by"),
					"notes":    str(take, "notes"),
					"status":   str(take, "status"),
				},
				"summary": map[string]any{
					"items_listed":       len(rows),
					"net_variance":       round2(net),
					"total_abs_variance": round2(abs),
					"shortages":          shortages,
					"surpluses":          countPositive(rows),
				},
				"items": rows[:min(100, len(rows))],
			}, nil
		},
	)
}

func countPositive(rows []map[string]any) int {
	n := 0
	for _, r := range rows {
		if r["variance"].(float64) > 0 {
			n++
		}
	}
	return n
}

func clampLimit(v, def, max int) int {
	if v <= 0 {
		return def
	}
	if v > max {
		return max
	}
	return v
}

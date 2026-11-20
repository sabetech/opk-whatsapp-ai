package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"opk-whatsapp-ai/internal/pocketbase"
)

// NewEmptiesStatusTool reports empties balances and recent activity.
func NewEmptiesStatusTool(env *Env) Tool {
	return newTool(
		"empties_status",
		"Empties (returnable crates/bottles) position: current quantity in trade and on the ground per product, plus empties activity "+
			"totals for a period (customer returns, purchases, reclaims, returns to supplier), excluding voided entries. Activity defaults to the last 30 days.",
		`{
			"type": "object",
			"properties": {`+periodParams+`
			},
			"additionalProperties": false
		}`,
		func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				From string `json:"from"`
				To   string `json:"to"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			if err := env.requireCollections("empties", "empties_log"); err != nil {
				return nil, err
			}

			p, err := env.parsePeriod(args.From, args.To, 30)
			if err != nil {
				return nil, err
			}

			balances, _, err := env.PB.ListAll(ctx, "empties", pocketbase.ListOptions{Expand: "product_id"}, maxScanRecords)
			if err != nil {
				return nil, err
			}

			var inTrade, onGround float64
			rows := make([]map[string]any, 0, len(balances))
			for _, b := range balances {
				t, g := num(b, "quantity_in_trade"), num(b, "quantity_on_ground")
				inTrade += t
				onGround += g
				rows = append(rows, map[string]any{
					"product":            str(b, "product_id.sku_name"),
					"quantity_in_trade":  t,
					"quantity_on_ground": g,
					"total":              t + g,
				})
			}
			sort.SliceStable(rows, func(i, j int) bool {
				return rows[i]["total"].(float64) > rows[j]["total"].(float64)
			})

			activity, err := env.aggregate(ctx, aggQuery{
				Collection: "empties_log",
				Filter:     and(p.filter("date"), "voided_at = ''"),
				GroupBy:    []string{"activity"},
				Metrics: []metricSpec{
					{Op: "sum", Field: "total_quantity"},
					{Op: "sum", Field: "deposit_qty"},
					{Op: "sum", Field: "deposit_total"},
					{Op: "sum", Field: "refund_qty"},
					{Op: "sum", Field: "refund_amount"},
				},
			})
			if err != nil {
				return nil, err
			}

			return map[string]any{
				"current_balances": map[string]any{
					"total_in_trade":  round2(inTrade),
					"total_on_ground": round2(onGround),
					"by_product":      rows,
				},
				"activity": map[string]any{
					"period":      p.describe(),
					"entries":     activity.Totals["count"],
					"by_activity": activity.Groups,
				},
			}, nil
		},
	)
}

// NewCrateDepositsOutstandingTool lists crate deposits still held per customer.
func NewCrateDepositsOutstandingTool(env *Env) Tool {
	return newTool(
		"crate_deposits_outstanding",
		"Crate deposits not yet fully refunded: outstanding crate quantity and value per customer (quantity - refunded_qty, "+
			"valued at amount_per_crate). Optionally for one customer.",
		`{
			"type": "object",
			"properties": {
				"customer": {"type": "string", "description": "Optional customer ID, name or phone (partial match)."},
				"limit": {"type": "integer", "minimum": 1, "maximum": 200, "description": "Maximum customers to list (default 50)."}
			},
			"additionalProperties": false
		}`,
		func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				Customer string `json:"customer"`
				Limit    int    `json:"limit"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			if err := env.requireCollections("crate_deposits", "customers"); err != nil {
				return nil, err
			}

			records, truncated, err := env.PB.ListAll(ctx, "crate_deposits", pocketbase.ListOptions{
				Filter: and("status != 'refunded'", customerClause("customer_id", args.Customer)),
				Expand: "customer_id",
			}, maxScanRecords)
			if err != nil {
				return nil, err
			}

			type row struct {
				name, phone string
				deposits    int
				qty, value  float64
				oldest      string
			}
			byCustomer := map[string]*row{}
			var totalQty, totalValue float64

			for _, d := range records {
				out := num(d, "quantity") - num(d, "refunded_qty")
				if out <= 0 {
					continue
				}
				id := str(d, "customer_id")
				r := byCustomer[id]
				if r == nil {
					r = &row{name: str(d, "customer_id.name"), phone: str(d, "customer_id.phone")}
					if r.name == "" {
						r.name = "(no customer)"
					}
					byCustomer[id] = r
				}
				created := env.localDate(getPath(d, "created"))
				if r.oldest == "" || (created != "" && created < r.oldest) {
					r.oldest = created
				}
				r.deposits++
				r.qty += out
				r.value += out * num(d, "amount_per_crate")
				totalQty += out
				totalValue += out * num(d, "amount_per_crate")
			}

			rows := make([]*row, 0, len(byCustomer))
			for _, r := range byCustomer {
				rows = append(rows, r)
			}
			sort.SliceStable(rows, func(i, j int) bool { return rows[i].value > rows[j].value })

			limit := clampLimit(args.Limit, 50, 200)
			list := make([]map[string]any, 0, min(limit, len(rows)))
			for i, r := range rows {
				if i >= limit {
					break
				}
				list = append(list, map[string]any{
					"customer":        r.name,
					"phone":           r.phone,
					"deposits":        r.deposits,
					"outstanding_qty": round2(r.qty),
					"value":           round2(r.value),
					"oldest_deposit":  r.oldest,
				})
			}

			result := map[string]any{
				"totals": map[string]any{
					"customers":       len(rows),
					"outstanding_qty": round2(totalQty),
					"value":           round2(totalValue),
				},
				"by_customer": list,
			}
			if len(rows) > limit {
				result["note"] = fmt.Sprintf("showing %d of %d customers", limit, len(rows))
			}
			if truncated {
				result["warning"] = "result set was truncated"
			}
			return result, nil
		},
	)
}

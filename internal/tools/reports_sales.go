package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"opk-whatsapp-ai/internal/llm"
	"opk-whatsapp-ai/internal/pocketbase"
)

func newTool(name, description, parameters string, handler Handler) Tool {
	return Tool{
		Definition: llm.ToolDefinition{
			Type: "function",
			Function: llm.FunctionSchema{
				Name:        name,
				Description: description,
				Parameters:  json.RawMessage(parameters),
			},
		},
		Handler: handler,
	}
}

const periodParams = `
	"from": {"type": "string", "description": "Start date YYYY-MM-DD (inclusive, business timezone)."},
	"to": {"type": "string", "description": "End date YYYY-MM-DD (inclusive). Defaults to today."}`

func orderStatusClause(field, status string) (string, error) {
	switch status {
	case "", "approved":
		return field + " = 'approved'", nil
	case "pending", "cancelled":
		return field + " = " + quote(status), nil
	case "all":
		return "", nil
	}
	return "", fmt.Errorf("invalid status %q", status)
}

// NewSalesSummaryTool reports revenue and volume for a period.
func NewSalesSummaryTool(env *Env) Tool {
	return newTool(
		"sales_summary",
		"Sales report for a period: number of orders, product sales revenue, crate deposits collected, order totals and units sold, "+
			"with an optional breakdown. Counts approved, non-deleted orders unless status says otherwise. Defaults to the last 30 days.",
		`{
			"type": "object",
			"properties": {`+periodParams+`,
				"group_by": {
					"type": "string",
					"enum": ["none", "day", "week", "month", "product", "customer", "customer_type", "payment_type", "order_type"],
					"description": "Breakdown dimension (default none)."
				},
				"status": {"type": "string", "enum": ["approved", "pending", "cancelled", "all"], "description": "Order status to include (default approved)."},
				"limit": {"type": "integer", "minimum": 1, "maximum": 200, "description": "Maximum breakdown rows (default 50)."}
			},
			"additionalProperties": false
		}`,
		func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				From    string `json:"from"`
				To      string `json:"to"`
				GroupBy string `json:"group_by"`
				Status  string `json:"status"`
				Limit   int    `json:"limit"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			if err := env.requireCollections("orders", "sales"); err != nil {
				return nil, err
			}

			p, err := env.parsePeriod(args.From, args.To, 30)
			if err != nil {
				return nil, err
			}

			orderStatus, err := orderStatusClause("status", args.Status)
			if err != nil {
				return nil, err
			}
			lineStatus, _ := orderStatusClause("order_id.status", args.Status)

			orderFilter := and(p.filter("date_time"), "deleted_at = ''", orderStatus)
			lineFilter := and(p.filter("order_id.date_time"), "deleted_at = ''", "order_id.deleted_at = ''", lineStatus)

			orderMetrics := []metricSpec{
				{Op: "sum", Field: "total_amount"},
				{Op: "sum", Field: "crate_deposit_total"},
				{Op: "sum", Field: "crate_deposit_qty"},
			}
			lineMetrics := []metricSpec{
				{Op: "sum", Field: "quantity"},
				{Op: "sum", Field: "sub_total"},
				{Op: "sum", Field: "discount"},
			}

			orders, err := env.aggregate(ctx, aggQuery{Collection: "orders", Filter: orderFilter, Metrics: orderMetrics})
			if err != nil {
				return nil, err
			}
			lines, err := env.aggregate(ctx, aggQuery{Collection: "sales", Filter: lineFilter, Metrics: lineMetrics})
			if err != nil {
				return nil, err
			}

			status := args.Status
			if status == "" {
				status = "approved"
			}

			result := map[string]any{
				"period":       p.describe(),
				"order_status": status,
				"totals": map[string]any{
					"orders":                    orders.Totals["count"],
					"product_sales_revenue":     lines.Totals["sum_sub_total"],
					"crate_deposit_total":       orders.Totals["sum_crate_deposit_total"],
					"crate_deposit_qty":         orders.Totals["sum_crate_deposit_qty"],
					"order_total_incl_deposits": orders.Totals["sum_total_amount"],
					"units_sold":                lines.Totals["sum_quantity"],
					"line_items":                lines.Totals["count"],
					"discounts":                 lines.Totals["sum_discount"],
				},
				"notes": "product_sales_revenue is the sum of product line sub_totals (actual sales). Order totals (total_amount) " +
					"include refundable crate deposits, so do not report them as sales revenue. In breakdowns by day, customer, " +
					"payment type or order type, sum_total_amount includes deposits; subtract sum_crate_deposit_total for sales.",
			}

			var breakdown *aggResult
			switch args.GroupBy {
			case "", "none":
			case "product":
				breakdown, err = env.aggregate(ctx, aggQuery{
					Collection: "sales", Filter: lineFilter, Metrics: lineMetrics,
					GroupBy: []string{"product_id.sku_name"}, Limit: args.Limit,
				})
			case "day", "week", "month":
				breakdown, err = env.aggregate(ctx, aggQuery{
					Collection: "orders", Filter: orderFilter, Metrics: orderMetrics,
					GroupBy: []string{"date_time:" + args.GroupBy}, SortBy: "group", Asc: true, Limit: args.Limit,
				})
			default:
				field := map[string]string{
					"customer":      "customer_id.name",
					"customer_type": "customer_id.type_id.name",
					"payment_type":  "payment_type",
					"order_type":    "order_type_id.name",
				}[args.GroupBy]
				if field == "" {
					return nil, fmt.Errorf("invalid group_by %q", args.GroupBy)
				}
				breakdown, err = env.aggregate(ctx, aggQuery{
					Collection: "orders", Filter: orderFilter, Metrics: orderMetrics,
					GroupBy: []string{field}, Limit: args.Limit,
				})
			}
			if err != nil {
				return nil, err
			}

			if breakdown != nil {
				result["group_by"] = args.GroupBy
				result["breakdown"] = breakdown.Groups
				result["total_groups"] = breakdown.TotalGroups
			}
			if orders.Truncated || lines.Truncated || (breakdown != nil && breakdown.Truncated) {
				result["warning"] = fmt.Sprintf("more than %d records matched; totals are partial", maxScanRecords)
			}

			return result, nil
		},
	)
}

// NewCustomerOverviewTool returns a 360° view of a single customer.
func NewCustomerOverviewTool(env *Env) Tool {
	return newTool(
		"customer_overview",
		"Full picture of one customer: details, balance, lifetime approved orders, recent orders, outstanding crate deposits "+
			"and recent empties activity. Accepts a customer ID, name or phone number (partial match).",
		`{
			"type": "object",
			"properties": {
				"customer": {"type": "string", "description": "Customer ID, name or phone number."}
			},
			"required": ["customer"],
			"additionalProperties": false
		}`,
		func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				Customer string `json:"customer"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			if err := env.requireCollections("customers", "orders", "crate_deposits", "empties_log"); err != nil {
				return nil, err
			}

			customer, candidates, err := env.resolveCustomer(ctx, args.Customer)
			if err != nil {
				return nil, err
			}
			if customer == nil {
				if len(candidates) == 0 {
					return map[string]any{"found": false, "message": "No customer matches " + quote(args.Customer)}, nil
				}
				return map[string]any{
					"found":            false,
					"multiple_matches": candidates,
					"message":          "Several customers match; ask the user which one they mean, then call again with the customer id.",
				}, nil
			}

			id := str(customer, "id")
			byCustomer := "customer_id = " + quote(id)

			lifetime, err := env.aggregate(ctx, aggQuery{
				Collection: "orders",
				Filter:     and(byCustomer, "deleted_at = ''"),
				GroupBy:    []string{"status"},
				Metrics:    []metricSpec{{Op: "sum", Field: "total_amount"}},
			})
			if err != nil {
				return nil, err
			}

			recent, err := env.PB.ListRecords(ctx, "orders", pocketbase.ListOptions{
				Filter:  and(byCustomer, "deleted_at = ''"),
				Sort:    "-date_time",
				PerPage: 10,
				Expand:  "order_type_id",
			})
			if err != nil {
				return nil, err
			}
			recentOrders := make([]map[string]any, 0, len(recent.Items))
			for _, o := range recent.Items {
				recentOrders = append(recentOrders, map[string]any{
					"order_number":      getPath(o, "order_number"),
					"date":              env.localDate(getPath(o, "date_time")),
					"status":            str(o, "status"),
					"order_type":        str(o, "order_type_id.name"),
					"total_amount":      num(o, "total_amount"),
					"payment_type":      str(o, "payment_type"),
					"crate_deposit_qty": num(o, "crate_deposit_qty"),
				})
			}

			deposits, _, err := env.PB.ListAll(ctx, "crate_deposits", pocketbase.ListOptions{Filter: byCustomer}, maxScanRecords)
			if err != nil {
				return nil, err
			}
			var heldQty, heldValue float64
			for _, d := range deposits {
				if str(d, "status") == "refunded" {
					continue
				}
				out := num(d, "quantity") - num(d, "refunded_qty")
				heldQty += out
				heldValue += out * num(d, "amount_per_crate")
			}

			emptiesFilter := and(byCustomer, "voided_at = ''")
			emptiesTotals, err := env.aggregate(ctx, aggQuery{
				Collection: "empties_log",
				Filter:     emptiesFilter,
				GroupBy:    []string{"activity"},
				Metrics: []metricSpec{
					{Op: "sum", Field: "total_quantity"},
					{Op: "sum", Field: "deposit_total"},
					{Op: "sum", Field: "refund_amount"},
				},
			})
			if err != nil {
				return nil, err
			}

			recentEmpties, err := env.PB.ListRecords(ctx, "empties_log", pocketbase.ListOptions{
				Filter:  emptiesFilter,
				Sort:    "-date",
				PerPage: 10,
			})
			if err != nil {
				return nil, err
			}
			emptiesActivity := make([]map[string]any, 0, len(recentEmpties.Items))
			for _, l := range recentEmpties.Items {
				emptiesActivity = append(emptiesActivity, map[string]any{
					"date":           env.localDate(getPath(l, "date")),
					"activity":       str(l, "activity"),
					"total_quantity": num(l, "total_quantity"),
					"deposit_total":  num(l, "deposit_total"),
					"refund_amount":  num(l, "refund_amount"),
				})
			}

			return map[string]any{
				"found": true,
				"customer": map[string]any{
					"id":      id,
					"name":    str(customer, "name"),
					"phone":   str(customer, "phone"),
					"type":    str(customer, "type_id.name"),
					"balance": num(customer, "balance"),
					"has_mou": getPath(customer, "has_mou"),
				},
				"orders_by_status":        lifetime.Groups,
				"recent_orders":           recentOrders,
				"crate_deposits_held":     map[string]any{"quantity": round2(heldQty), "value": round2(heldValue)},
				"empties_by_activity":     emptiesTotals.Groups,
				"recent_empties_activity": emptiesActivity,
			}, nil
		},
	)
}

// resolveCustomer finds exactly one active customer by ID, name or phone.
// If several match and none is exact, it returns candidates instead.
func (e *Env) resolveCustomer(ctx context.Context, query string) (map[string]any, []map[string]any, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil, fmt.Errorf("customer is required")
	}

	if isRecordID(query) {
		rec, err := e.PB.GetRecord(ctx, "customers", query, "type_id")
		if err == nil {
			return rec, nil, nil
		}
	}

	res, err := e.PB.ListRecords(ctx, "customers", pocketbase.ListOptions{
		Filter:  and("deleted_at = ''", customerClause("", query)),
		Expand:  "type_id",
		PerPage: 10,
		Sort:    "name",
	})
	if err != nil {
		return nil, nil, err
	}

	if len(res.Items) == 1 {
		return res.Items[0], nil, nil
	}

	for _, c := range res.Items {
		if strings.EqualFold(str(c, "name"), query) || str(c, "phone") == query {
			return c, nil, nil
		}
	}

	candidates := make([]map[string]any, 0, len(res.Items))
	for _, c := range res.Items {
		candidates = append(candidates, map[string]any{
			"id":      str(c, "id"),
			"name":    str(c, "name"),
			"phone":   str(c, "phone"),
			"type":    str(c, "type_id.name"),
			"balance": num(c, "balance"),
		})
	}
	return nil, candidates, nil
}

package tools

import (
	"context"
	"encoding/json"
	"sort"
)

// NewVSEPerformanceTool summarises activity per VSE (van sales executive).
func NewVSEPerformanceTool(env *Env) Tool {
	return newTool(
		"vse_performance",
		"Performance per VSE (van sales executive) for a period: approved loadouts and units loaded, field sales by status, "+
			"approved field-sales revenue and units, approved empties received, and units sold/returned from VSE movements. "+
			"Optionally for one VSE (ID, name or phone). Defaults to the last 30 days.",
		`{
			"type": "object",
			"properties": {`+periodParams+`,
				"vse": {"type": "string", "description": "Optional VSE customer ID, name or phone (partial match)."}
			},
			"additionalProperties": false
		}`,
		func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				From string `json:"from"`
				To   string `json:"to"`
				VSE  string `json:"vse"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			if err := env.requireCollections("loadouts", "loadout_items", "vse_field_sales", "vse_field_sale_items", "vse_movements"); err != nil {
				return nil, err
			}

			p, err := env.parsePeriod(args.From, args.To, 30)
			if err != nil {
				return nil, err
			}

			stats := map[string]map[string]any{}
			row := func(name string) map[string]any {
				r := stats[name]
				if r == nil {
					r = map[string]any{
						"vse":                   name,
						"loadouts":              0,
						"units_loaded":          0.0,
						"field_sales_by_status": map[string]any{},
						"field_sales_revenue":   0.0,
						"field_sales_units":     0.0,
						"empties_received":      0.0,
						"units_sold_movements":  0.0,
						"units_returned":        0.0,
					}
					stats[name] = r
				}
				return r
			}

			type step struct {
				q     aggQuery
				apply func(group map[string]any, g map[string]any)
			}

			steps := []step{
				{
					q: aggQuery{
						Collection: "loadouts",
						Filter:     and(p.filter("date"), "status = 'approved'", customerClause("vse_id", args.VSE)),
						GroupBy:    []string{"vse_id.name"},
					},
					apply: func(group, g map[string]any) {
						row(group["vse_id.name"].(string))["loadouts"] = g["count"]
					},
				},
				{
					q: aggQuery{
						Collection: "loadout_items",
						Filter:     and(p.filter("loadout_id.date"), "loadout_id.status = 'approved'", customerClause("loadout_id.vse_id", args.VSE)),
						GroupBy:    []string{"loadout_id.vse_id.name"},
						Metrics:    []metricSpec{{Op: "sum", Field: "quantity"}},
					},
					apply: func(group, g map[string]any) {
						row(group["loadout_id.vse_id.name"].(string))["units_loaded"] = g["sum_quantity"]
					},
				},
				{
					q: aggQuery{
						Collection: "vse_field_sales",
						Filter:     and(p.filter("date"), customerClause("vse_customer_id", args.VSE)),
						GroupBy:    []string{"vse_customer_id.name", "sale_status"},
					},
					apply: func(group, g map[string]any) {
						r := row(group["vse_customer_id.name"].(string))
						r["field_sales_by_status"].(map[string]any)[group["sale_status"].(string)] = g["count"]
					},
				},
				{
					q: aggQuery{
						Collection: "vse_field_sale_items",
						Filter:     and(p.filter("sale_id.date"), "sale_id.sale_status = 'approved'", customerClause("sale_id.vse_customer_id", args.VSE)),
						GroupBy:    []string{"sale_id.vse_customer_id.name"},
						Metrics:    []metricSpec{{Op: "sum", Field: "sub_total"}, {Op: "sum", Field: "quantity"}},
					},
					apply: func(group, g map[string]any) {
						r := row(group["sale_id.vse_customer_id.name"].(string))
						r["field_sales_revenue"] = g["sum_sub_total"]
						r["field_sales_units"] = g["sum_quantity"]
					},
				},
				{
					q: aggQuery{
						Collection: "vse_field_sales",
						Filter:     and(p.filter("date"), "empties_status = 'approved'", customerClause("vse_customer_id", args.VSE)),
						GroupBy:    []string{"vse_customer_id.name"},
						Metrics:    []metricSpec{{Op: "sum", Field: "empties_received"}},
					},
					apply: func(group, g map[string]any) {
						row(group["vse_customer_id.name"].(string))["empties_received"] = g["sum_empties_received"]
					},
				},
				{
					q: aggQuery{
						Collection: "vse_movements",
						Filter:     and(p.filter("date"), customerClause("vse_id", args.VSE)),
						GroupBy:    []string{"vse_id.name", "movement_type"},
						Metrics:    []metricSpec{{Op: "sum", Field: "quantity"}},
					},
					apply: func(group, g map[string]any) {
						r := row(group["vse_id.name"].(string))
						switch group["movement_type"] {
						case "sold":
							r["units_sold_movements"] = g["sum_quantity"]
						case "returned":
							r["units_returned"] = g["sum_quantity"]
						}
					},
				},
			}

			for _, s := range steps {
				s.q.Limit = maxGroupLimit
				res, err := env.aggregate(ctx, s.q)
				if err != nil {
					return nil, err
				}
				for _, g := range res.Groups {
					s.apply(g["group"].(map[string]any), g)
				}
			}

			rows := make([]map[string]any, 0, len(stats))
			for _, r := range stats {
				rows = append(rows, r)
			}
			sort.SliceStable(rows, func(i, j int) bool {
				ri, _ := toFloat(rows[i]["field_sales_revenue"])
				rj, _ := toFloat(rows[j]["field_sales_revenue"])
				if ri == rj {
					return rows[i]["vse"].(string) < rows[j]["vse"].(string)
				}
				return ri > rj
			})

			return map[string]any{
				"period": p.describe(),
				"vses":   rows,
				"notes":  "field_sales_revenue/units count approved field sales only; empties_received counts approved empties only.",
			}, nil
		},
	)
}

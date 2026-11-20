package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"opk-whatsapp-ai/internal/llm"
	"opk-whatsapp-ai/internal/pocketbase"
)

const (
	maxScanRecords     = 20000
	defaultGroupLimit  = 50
	maxGroupLimit      = 200
	maxGroupByFields   = 3
	maxAggregateMetric = 6
)

var (
	pathPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*){0,4}(:(day|week|month|year))?$`)
	validOps    = map[string]bool{"count": true, "sum": true, "avg": true, "min": true, "max": true}
)

type metricSpec struct {
	Op    string `json:"op"`
	Field string `json:"field"`
}

func (m metricSpec) key() string {
	if m.Op == "count" {
		return "count"
	}
	return m.Op + "_" + strings.ReplaceAll(m.Field, ".", "_")
}

type aggQuery struct {
	Collection string
	Filter     string
	GroupBy    []string
	Metrics    []metricSpec
	// SortBy is a metric key, "count", or "group". Empty sorts by the first
	// metric (or count).
	SortBy string
	Asc    bool
	Limit  int
}

type aggResult struct {
	RecordsScanned int              `json:"records_scanned"`
	Truncated      bool             `json:"truncated,omitempty"`
	Totals         map[string]any   `json:"totals"`
	TotalGroups    int              `json:"total_groups,omitempty"`
	Groups         []map[string]any `json:"groups,omitempty"`
}

type accum struct {
	group map[string]any
	key   string
	count int
	sum   map[string]float64
	min   map[string]float64
	max   map[string]float64
	n     map[string]int
}

func newAccum(group map[string]any, key string) *accum {
	return &accum{
		group: group,
		key:   key,
		sum:   map[string]float64{},
		min:   map[string]float64{},
		max:   map[string]float64{},
		n:     map[string]int{},
	}
}

func (a *accum) add(rec map[string]any, metrics []metricSpec) {
	a.count++
	for _, m := range metrics {
		if m.Op == "count" {
			continue
		}
		v, ok := numAt(rec, m.Field)
		if !ok {
			continue
		}
		f := m.Field
		if a.n[f] == 0 || v < a.min[f] {
			a.min[f] = v
		}
		if a.n[f] == 0 || v > a.max[f] {
			a.max[f] = v
		}
		a.sum[f] += v
		a.n[f]++
	}
}

func (a *accum) values(metrics []metricSpec) map[string]any {
	out := map[string]any{"count": a.count}
	for _, m := range metrics {
		f := m.Field
		switch m.Op {
		case "sum":
			out[m.key()] = round2(a.sum[f])
		case "avg":
			if a.n[f] > 0 {
				out[m.key()] = round2(a.sum[f] / float64(a.n[f]))
			} else {
				out[m.key()] = nil
			}
		case "min":
			if a.n[f] > 0 {
				out[m.key()] = round2(a.min[f])
			} else {
				out[m.key()] = nil
			}
		case "max":
			if a.n[f] > 0 {
				out[m.key()] = round2(a.max[f])
			} else {
				out[m.key()] = nil
			}
		}
	}
	return out
}

func validatePath(path string) error {
	if !pathPattern.MatchString(path) {
		return fmt.Errorf("invalid field path %q", path)
	}
	base, _, _ := strings.Cut(path, ":")
	segs := strings.Split(base, ".")
	if sensitiveFields[segs[len(segs)-1]] {
		return fmt.Errorf("field %q cannot be used", path)
	}
	return nil
}

// expandFor returns the PocketBase expand needed to resolve the given paths.
func expandFor(paths []string) string {
	seen := map[string]bool{}
	var expands []string
	for _, p := range paths {
		base, _, _ := strings.Cut(p, ":")
		segs := strings.Split(base, ".")
		if len(segs) < 2 {
			continue
		}
		e := strings.Join(segs[:len(segs)-1], ".")
		if !seen[e] {
			seen[e] = true
			expands = append(expands, e)
		}
	}
	return strings.Join(expands, ",")
}

// groupValue returns the grouping label for path, applying an optional
// date granularity suffix (:day, :week, :month, :year).
func (e *Env) groupValue(rec map[string]any, path string) string {
	base, gran, _ := strings.Cut(path, ":")
	v := getPath(rec, base)

	if gran != "" {
		s, _ := v.(string)
		t, ok := parsePBTime(s)
		if !ok {
			return "(none)"
		}
		t = t.In(e.Loc)
		switch gran {
		case "day":
			return t.Format(dateLayout)
		case "week":
			y, w := t.ISOWeek()
			return fmt.Sprintf("%d-W%02d", y, w)
		case "month":
			return t.Format("2006-01")
		case "year":
			return t.Format("2006")
		}
	}

	switch t := v.(type) {
	case nil:
		return "(none)"
	case string:
		if t == "" {
			return "(none)"
		}
		return t
	case []any:
		if len(t) == 0 {
			return "(none)"
		}
		return fmt.Sprint(t...)
	default:
		return fmt.Sprint(t)
	}
}

// aggregate scans matching records and computes grouped metrics in memory.
func (e *Env) aggregate(ctx context.Context, q aggQuery) (*aggResult, error) {
	paths := append([]string{}, q.GroupBy...)
	for _, m := range q.Metrics {
		if m.Op != "count" {
			paths = append(paths, m.Field)
		}
	}

	records, truncated, err := e.PB.ListAll(ctx, q.Collection, pocketbase.ListOptions{
		Filter: q.Filter,
		Expand: expandFor(paths),
	}, maxScanRecords)
	if err != nil {
		return nil, err
	}

	total := newAccum(nil, "")
	groups := map[string]*accum{}

	for _, rec := range records {
		total.add(rec, q.Metrics)

		if len(q.GroupBy) == 0 {
			continue
		}

		labels := make(map[string]any, len(q.GroupBy))
		parts := make([]string, len(q.GroupBy))
		for i, p := range q.GroupBy {
			parts[i] = e.groupValue(rec, p)
			labels[p] = parts[i]
		}

		key := strings.Join(parts, "\x1f")
		a := groups[key]
		if a == nil {
			a = newAccum(labels, key)
			groups[key] = a
		}
		a.add(rec, q.Metrics)
	}

	result := &aggResult{
		RecordsScanned: len(records),
		Truncated:      truncated,
		Totals:         total.values(q.Metrics),
	}

	if len(q.GroupBy) == 0 {
		return result, nil
	}

	sortBy := q.SortBy
	if sortBy == "" {
		sortBy = "count"
		for _, m := range q.Metrics {
			if m.Op != "count" {
				sortBy = m.key()
				break
			}
		}
	}

	rows := make([]*accum, 0, len(groups))
	for _, a := range groups {
		rows = append(rows, a)
	}

	values := make(map[*accum]map[string]any, len(rows))
	for _, a := range rows {
		values[a] = a.values(q.Metrics)
	}

	sort.SliceStable(rows, func(i, j int) bool {
		if sortBy == "group" {
			if q.Asc {
				return rows[i].key < rows[j].key
			}
			return rows[i].key > rows[j].key
		}
		vi, _ := toFloat(values[rows[i]][sortBy])
		vj, _ := toFloat(values[rows[j]][sortBy])
		if vi == vj {
			return rows[i].key < rows[j].key
		}
		if q.Asc {
			return vi < vj
		}
		return vi > vj
	})

	limit := q.Limit
	if limit <= 0 {
		limit = defaultGroupLimit
	}
	if limit > maxGroupLimit {
		limit = maxGroupLimit
	}

	result.TotalGroups = len(rows)
	for i, a := range rows {
		if i >= limit {
			break
		}
		row := map[string]any{"group": a.group}
		for k, v := range values[a] {
			row[k] = v
		}
		result.Groups = append(result.Groups, row)
	}

	return result, nil
}

func toFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case int:
		return float64(t), true
	}
	return 0, false
}

// NewAggregateTool exposes the aggregation engine to the model.
func NewAggregateTool(env *Env) Tool {
	return Tool{
		Definition: llm.ToolDefinition{
			Type: "function",
			Function: llm.FunctionSchema{
				Name: "aggregate_records",
				Description: "Computes count/sum/avg/min/max over records in a collection, optionally grouped by up to 3 fields. " +
					"Fields may traverse relations with dots (e.g. product_id.sku_name, order_id.customer_id.name). " +
					"Date fields can be bucketed with a suffix: date_time:day, :week, :month or :year. " +
					"Prefer the specific report tools (sales_summary, stock_status, etc.) when one fits; use this for anything else.",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"collection": {"type": "string", "description": "Collection name, as returned by list_collections."},
						"filter": {"type": "string", "description": "Optional PocketBase filter, e.g. deleted_at = '' && date >= '2026-11-01 00:00:00'."},
						"group_by": {
							"type": "array",
							"items": {"type": "string"},
							"maxItems": 3,
							"description": "Fields to group by, e.g. [\"product_id.sku_name\"] or [\"date:month\", \"type\"]."
						},
						"metrics": {
							"type": "array",
							"maxItems": 6,
							"items": {
								"type": "object",
								"properties": {
									"op": {"type": "string", "enum": ["count", "sum", "avg", "min", "max"]},
									"field": {"type": "string", "description": "Numeric field (not needed for count)."}
								},
								"required": ["op"],
								"additionalProperties": false
							},
							"description": "Metrics to compute. The record count is always included."
						},
						"sort_by": {"type": "string", "description": "Metric key to sort groups by (e.g. sum_quantity, count) or 'group'. Defaults to the first metric."},
						"ascending": {"type": "boolean", "description": "Sort ascending instead of descending."},
						"limit": {"type": "integer", "minimum": 1, "maximum": 200, "description": "Maximum groups to return (default 50)."}
					},
					"required": ["collection"],
					"additionalProperties": false
				}`),
			},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				Collection string       `json:"collection"`
				Filter     string       `json:"filter"`
				GroupBy    []string     `json:"group_by"`
				Metrics    []metricSpec `json:"metrics"`
				SortBy     string       `json:"sort_by"`
				Ascending  bool         `json:"ascending"`
				Limit      int          `json:"limit"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return nil, err
			}
			if err := env.Access.Check(args.Collection); err != nil {
				return nil, err
			}
			if len(args.GroupBy) > maxGroupByFields {
				return nil, fmt.Errorf("group_by supports at most %d fields", maxGroupByFields)
			}
			if len(args.Metrics) > maxAggregateMetric {
				return nil, fmt.Errorf("at most %d metrics are supported", maxAggregateMetric)
			}
			for _, p := range args.GroupBy {
				if err := validatePath(p); err != nil {
					return nil, err
				}
			}
			for _, m := range args.Metrics {
				if !validOps[m.Op] {
					return nil, fmt.Errorf("unsupported metric op %q", m.Op)
				}
				if m.Op == "count" {
					continue
				}
				if m.Field == "" {
					return nil, fmt.Errorf("metric %q requires a field", m.Op)
				}
				if strings.Contains(m.Field, ":") {
					return nil, fmt.Errorf("metric field %q cannot use a date suffix", m.Field)
				}
				if err := validatePath(m.Field); err != nil {
					return nil, err
				}
			}

			result, err := env.aggregate(ctx, aggQuery{
				Collection: args.Collection,
				Filter:     args.Filter,
				GroupBy:    args.GroupBy,
				Metrics:    args.Metrics,
				SortBy:     args.SortBy,
				Asc:        args.Ascending,
				Limit:      args.Limit,
			})
			if err != nil {
				return nil, err
			}

			return map[string]any{
				"collection": args.Collection,
				"filter":     args.Filter,
				"result":     result,
			}, nil
		},
	}
}

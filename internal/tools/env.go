package tools

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"opk-whatsapp-ai/internal/pocketbase"
)

// Env holds the shared dependencies used by data tools.
type Env struct {
	PB     *pocketbase.Client
	Access *CollectionAccess
	Loc    *time.Location
	Now    func() time.Time
}

func NewEnv(pb *pocketbase.Client, access *CollectionAccess, loc *time.Location) *Env {
	return &Env{PB: pb, Access: access, Loc: loc, Now: time.Now}
}

// All returns every tool the agent can use.
func All(env *Env) []Tool {
	return []Tool{
		NewPocketBaseHealthTool(env.PB),
		NewListCollectionsTool(env.PB, env.Access),
		NewCountTool(env.PB, env.Access),
		NewSearchTool(env.PB, env.Access),
		NewRetrieveTool(env.PB, env.Access),
		NewAggregateTool(env),
		NewSalesSummaryTool(env),
		NewCustomerOverviewTool(env),
		NewStockStatusTool(env),
		NewStockMovementsTool(env),
		NewEmptiesStatusTool(env),
		NewCrateDepositsOutstandingTool(env),
		NewVSEPerformanceTool(env),
		NewPendingApprovalsTool(env),
		NewStockTakeVariancesTool(env),
	}
}

// requireCollections ensures every collection a report reads is permitted.
func (e *Env) requireCollections(names ...string) error {
	for _, name := range names {
		if err := e.Access.Check(name); err != nil {
			return err
		}
	}
	return nil
}

// ---- Dates ---------------------------------------------------------------

const dateLayout = "2006-01-02"

// period is a half-open time range [From, To) in the business timezone.
type period struct {
	From time.Time
	To   time.Time
}

// parsePeriod parses inclusive YYYY-MM-DD dates in the business timezone.
// Missing values default to the last defaultDays days ending today.
func (e *Env) parsePeriod(from, to string, defaultDays int) (period, error) {
	now := e.Now().In(e.Loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, e.Loc)

	end := today
	if to != "" {
		t, err := time.ParseInLocation(dateLayout, to, e.Loc)
		if err != nil {
			return period{}, fmt.Errorf("invalid 'to' date %q: use YYYY-MM-DD", to)
		}
		end = t
	}

	start := end.AddDate(0, 0, -(defaultDays - 1))
	if from != "" {
		t, err := time.ParseInLocation(dateLayout, from, e.Loc)
		if err != nil {
			return period{}, fmt.Errorf("invalid 'from' date %q: use YYYY-MM-DD", from)
		}
		start = t
	}

	if start.After(end) {
		return period{}, fmt.Errorf("'from' (%s) is after 'to' (%s)", start.Format(dateLayout), end.Format(dateLayout))
	}

	return period{From: start, To: end.AddDate(0, 0, 1)}, nil
}

// filter returns a PocketBase filter restricting field to the period.
func (p period) filter(field string) string {
	return fmt.Sprintf("%s >= '%s' && %s < '%s'", field, pbTime(p.From), field, pbTime(p.To))
}

func (p period) describe() map[string]string {
	return map[string]string{
		"from": p.From.Format(dateLayout),
		"to":   p.To.AddDate(0, 0, -1).Format(dateLayout),
	}
}

func pbTime(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04:05")
}

func parsePBTime(s string) (time.Time, bool) {
	for _, layout := range []string{"2006-01-02 15:04:05.000Z", "2006-01-02 15:04:05Z", time.RFC3339, dateLayout} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// localDate formats a PocketBase datetime as YYYY-MM-DD in the business timezone.
func (e *Env) localDate(v any) string {
	s, _ := v.(string)
	if t, ok := parsePBTime(s); ok {
		return t.In(e.Loc).Format(dateLayout)
	}
	return s
}

// ---- Filters -------------------------------------------------------------

var recordIDPattern = regexp.MustCompile(`^[a-z0-9]{15}$`)

func isRecordID(s string) bool { return recordIDPattern.MatchString(s) }

// quote returns s as a safely escaped PocketBase filter string literal.
func quote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return "'" + s + "'"
}

// and joins non-empty filter clauses with &&.
func and(clauses ...string) string {
	var parts []string
	for _, c := range clauses {
		if c = strings.TrimSpace(c); c != "" {
			parts = append(parts, "("+c+")")
		}
	}
	return strings.Join(parts, " && ")
}

// matchClause matches a related record (rel, e.g. "product_id") by exact ID
// or by partial match on any of fields. rel == "" matches the record itself.
func matchClause(rel, query string, fields ...string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return ""
	}

	if isRecordID(query) {
		if rel == "" {
			return "id = " + quote(query)
		}
		return rel + " = " + quote(query)
	}

	prefix := ""
	if rel != "" {
		prefix = rel + "."
	}

	ors := make([]string, len(fields))
	for i, f := range fields {
		ors[i] = prefix + f + " ~ " + quote(query)
	}
	return strings.Join(ors, " || ")
}

func productClause(rel, query string) string {
	return matchClause(rel, query, "sku_name", "code_name", "product_code")
}

func customerClause(rel, query string) string {
	return matchClause(rel, query, "name", "phone")
}

// ---- Record access -------------------------------------------------------

// getPath reads a (possibly relation-traversing) dotted path from a record
// fetched with the matching expand, e.g. "order_id.customer_id.name".
func getPath(rec map[string]any, path string) any {
	segs := strings.Split(path, ".")
	cur := rec

	for i, seg := range segs {
		if i == len(segs)-1 {
			return cur[seg]
		}

		expand, _ := cur["expand"].(map[string]any)
		if expand == nil {
			return nil
		}

		switch v := expand[seg].(type) {
		case map[string]any:
			cur = v
		case []any:
			if len(v) == 0 {
				return nil
			}
			m, ok := v[0].(map[string]any)
			if !ok {
				return nil
			}
			cur = m
		default:
			return nil
		}
	}
	return nil
}

func numAt(rec map[string]any, path string) (float64, bool) {
	switch v := getPath(rec, path).(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case string:
		f, err := strconv.ParseFloat(v, 64)
		return f, err == nil
	}
	return 0, false
}

func num(rec map[string]any, path string) float64 {
	v, _ := numAt(rec, path)
	return v
}

func str(rec map[string]any, path string) string {
	switch v := getPath(rec, path).(type) {
	case nil:
		return ""
	case string:
		return v
	default:
		return fmt.Sprint(v)
	}
}

func round2(f float64) float64 {
	return math.Round(f*100) / 100
}

// sensitiveFields are never returned to the model.
var sensitiveFields = map[string]bool{
	"email":        true,
	"password":     true,
	"passwordHash": true,
	"tokenKey":     true,
}

// redact recursively removes sensitive fields (including inside expand).
func redact(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if sensitiveFields[k] {
				delete(t, k)
				continue
			}
			redact(val)
		}
	case []any:
		for _, item := range t {
			redact(item)
		}
	case []map[string]any:
		for _, item := range t {
			redact(item)
		}
	}
	return v
}

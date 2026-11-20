package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"opk-whatsapp-ai/internal/pocketbase"
)

func testEnv(t *testing.T, records map[string][]map[string]any) (*Env, *[]string) {
	t.Helper()
	var filters []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/collections/_superusers/auth-with-password":
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "t"})
		default:
			filters = append(filters, r.URL.Query().Get("filter"))
			var name string
			if _, err := fmtSscanf(r.URL.Path, &name); err != nil {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"page": 1, "items": records[name]})
		}
	}))
	t.Cleanup(srv.Close)

	pb := pocketbase.NewClient(srv.URL)
	if err := pb.Authenticate(context.Background(), "a@b.c", "x"); err != nil {
		t.Fatal(err)
	}

	env := NewEnv(pb, NewCollectionAccess(nil), time.UTC)
	env.Now = func() time.Time { return time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC) }
	return env, &filters
}

// fmtSscanf extracts the collection name from /api/collections/{name}/records.
func fmtSscanf(path string, name *string) (int, error) {
	const prefix, suffix = "/api/collections/", "/records"
	if len(path) <= len(prefix)+len(suffix) || path[:len(prefix)] != prefix || path[len(path)-len(suffix):] != suffix {
		return 0, http.ErrNotSupported
	}
	*name = path[len(prefix) : len(path)-len(suffix)]
	return 1, nil
}

func line(product string, qty, subTotal float64, date string) map[string]any {
	return map[string]any{
		"quantity":  qty,
		"sub_total": subTotal,
		"expand": map[string]any{
			"product_id": map[string]any{"sku_name": product},
			"order_id":   map[string]any{"date_time": date},
		},
	}
}

func TestAggregateGroupsAndSorts(t *testing.T) {
	env, _ := testEnv(t, map[string][]map[string]any{
		"sales": {
			line("Beer", 2, 20, "2026-11-01 08:00:00.000Z"),
			line("Beer", 3, 30, "2026-11-02 08:00:00.000Z"),
			line("Malt", 10, 50, "2026-11-02 09:00:00.000Z"),
			line("Water", 1, 2, "2026-11-03 09:00:00.000Z"),
		},
	})

	res, err := env.aggregate(context.Background(), aggQuery{
		Collection: "sales",
		GroupBy:    []string{"product_id.sku_name"},
		Metrics:    []metricSpec{{Op: "sum", Field: "sub_total"}, {Op: "avg", Field: "quantity"}, {Op: "max", Field: "quantity"}},
		Limit:      2,
	})
	if err != nil {
		t.Fatal(err)
	}

	if res.RecordsScanned != 4 || res.TotalGroups != 3 || len(res.Groups) != 2 {
		t.Fatalf("scanned=%d groups=%d returned=%d", res.RecordsScanned, res.TotalGroups, len(res.Groups))
	}
	if res.Totals["sum_sub_total"] != 102.0 {
		t.Errorf("total sum = %v", res.Totals["sum_sub_total"])
	}

	top := res.Groups[0]
	if top["group"].(map[string]any)["product_id.sku_name"] != "Beer" || top["sum_sub_total"] != 50.0 || top["count"] != 2 {
		t.Errorf("top group = %v", top)
	}
	if top["avg_quantity"] != 2.5 || top["max_quantity"] != 3.0 {
		t.Errorf("avg/max = %v/%v", top["avg_quantity"], top["max_quantity"])
	}
}

func TestAggregateDateBuckets(t *testing.T) {
	env, _ := testEnv(t, map[string][]map[string]any{
		"sales": {
			line("Beer", 2, 20, "2026-11-01 08:00:00.000Z"),
			line("Beer", 3, 30, "2026-11-02 08:00:00.000Z"),
			line("Malt", 10, 50, "2026-11-02 09:00:00.000Z"),
		},
	})

	res, err := env.aggregate(context.Background(), aggQuery{
		Collection: "sales",
		GroupBy:    []string{"order_id.date_time:day"},
		Metrics:    []metricSpec{{Op: "sum", Field: "quantity"}},
		SortBy:     "group",
		Asc:        true,
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(res.Groups) != 2 {
		t.Fatalf("groups = %v", res.Groups)
	}
	first := res.Groups[0]
	if first["group"].(map[string]any)["order_id.date_time:day"] != "2026-11-01" || first["sum_quantity"] != 2.0 {
		t.Errorf("first = %v", first)
	}
	if res.Groups[1]["sum_quantity"] != 13.0 {
		t.Errorf("second = %v", res.Groups[1])
	}
}

func TestParsePeriod(t *testing.T) {
	env, _ := testEnv(t, nil)

	p, err := env.parsePeriod("", "", 30)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.describe(); got["from"] != "2026-10-22" || got["to"] != "2026-11-20" {
		t.Errorf("default period = %v", got)
	}
	if f := p.filter("date"); f != "date >= '2026-10-22 00:00:00' && date < '2026-11-21 00:00:00'" {
		t.Errorf("filter = %s", f)
	}

	if _, err := env.parsePeriod("2026-11-10", "2026-11-01", 30); err == nil {
		t.Error("expected error for from > to")
	}
	if _, err := env.parsePeriod("10/11/2026", "", 30); err == nil {
		t.Error("expected error for bad date format")
	}
}

func TestFilterHelpers(t *testing.T) {
	if got := quote(`O'Brien \ x`); got != `'O\'Brien \\ x'` {
		t.Errorf("quote = %s", got)
	}
	if got := and("a = 1", "", "b = 2"); got != "(a = 1) && (b = 2)" {
		t.Errorf("and = %s", got)
	}
	if got := customerClause("customer_id", "abcdefghij12345"); got != "customer_id = 'abcdefghij12345'" {
		t.Errorf("id clause = %s", got)
	}
	if got := customerClause("customer_id", "Kofi"); got != "customer_id.name ~ 'Kofi' || customer_id.phone ~ 'Kofi'" {
		t.Errorf("name clause = %s", got)
	}
	if got := customerClause("", ""); got != "" {
		t.Errorf("empty clause = %s", got)
	}
}

func TestRedactAndPathValidation(t *testing.T) {
	rec := map[string]any{
		"name":  "A",
		"email": "a@b.c",
		"expand": map[string]any{
			"created_by": map[string]any{"name": "B", "email": "b@c.d", "tokenKey": "x"},
		},
	}
	redact(rec)
	if _, ok := rec["email"]; ok {
		t.Error("email not redacted")
	}
	inner := rec["expand"].(map[string]any)["created_by"].(map[string]any)
	if _, ok := inner["email"]; ok || inner["tokenKey"] != nil {
		t.Error("nested sensitive fields not redacted")
	}

	for _, bad := range []string{"email", "created_by.email", "a;drop", "x:hour", ""} {
		if validatePath(bad) == nil {
			t.Errorf("validatePath(%q) should fail", bad)
		}
	}
	for _, good := range []string{"status", "product_id.sku_name", "date_time:month", "order_id.customer_id.type_id.name"} {
		if err := validatePath(good); err != nil {
			t.Errorf("validatePath(%q) = %v", good, err)
		}
	}
}

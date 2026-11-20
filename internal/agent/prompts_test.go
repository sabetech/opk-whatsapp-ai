package agent

import (
	"strings"
	"testing"
	"time"
)

func TestBuildSystemPrompt(t *testing.T) {
	now := time.Date(2026, 11, 20, 9, 30, 0, 0, time.UTC)

	withCurrency := buildSystemPrompt(now, "GHS")
	if !strings.Contains(withCurrency, "All monetary amounts are in GHS") {
		t.Error("currency rule missing")
	}
	if !strings.Contains(withCurrency, "Friday, 20 November 2026 09:30") {
		t.Error("current date missing")
	}

	without := buildSystemPrompt(now, "")
	if !strings.Contains(without, "do not name or assume a currency") {
		t.Error("no-currency rule missing")
	}
}

func TestCallerPrompt(t *testing.T) {
	got := callerPrompt(Caller{Name: "Ama", Role: "sales_manager", Channel: "whatsapp"})
	if !strings.Contains(got, "talking to Ama (role: sales_manager)") || !strings.Contains(got, "Never use Markdown tables") {
		t.Errorf("callerPrompt = %q", got)
	}
	if callerPrompt(Caller{}) != "" {
		t.Error("empty caller should add nothing")
	}
}

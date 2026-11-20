package tools

import "testing"

func TestCollectionAccess(t *testing.T) {
	open := NewCollectionAccess(nil)
	restricted := NewCollectionAccess([]string{"customers", "sales"})

	cases := []struct {
		access *CollectionAccess
		name   string
		want   bool
	}{
		{open, "customers", true},
		{open, "_superusers", false},
		{open, "_mfas", false},
		{open, "../_superusers", false},
		{open, "customers/records", false},
		{open, "", false},
		{open, "app_settings", false},
		{NewCollectionAccess([]string{"app_settings"}), "app_settings", false},
		{restricted, "customers", true},
		{restricted, "inventory", false},
		{restricted, "_superusers", false},
	}

	for _, tc := range cases {
		if got := tc.access.Allowed(tc.name); got != tc.want {
			t.Errorf("Allowed(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

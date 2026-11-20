package phone

import "testing"

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"233241234567":     "233241234567",
		"+233 24 123 4567": "233241234567",
		"+233-24-123-4567": "233241234567",
		"00233241234567":   "233241234567",
		"0241234567":       "233241234567",
		"024 123 4567":     "233241234567",
		"241234567":        "233241234567",
		"+447911123456":    "447911123456",
		"":                 "",
		"abc":              "",
	}
	for in, want := range cases {
		if got := Normalize(in, "233"); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMask(t *testing.T) {
	if got := Mask("233241234567"); got != "********4567" {
		t.Errorf("Mask = %q", got)
	}
}

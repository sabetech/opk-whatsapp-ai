// Package phone normalizes phone numbers (MSISDNs) for comparison.
package phone

import "strings"

// Normalize converts a phone number to international digits without "+"
// (the format WhatsApp uses for wa_id / "from"), e.g. 233241234567.
//
// countryCode (e.g. "233") is applied to local numbers:
//
//	+233 24 123 4567 -> 233241234567
//	00233241234567   -> 233241234567
//	0241234567       -> 233241234567
//	241234567        -> 233241234567
//	233241234567     -> 233241234567
//
// It returns "" if the input contains no digits.
func Normalize(raw, countryCode string) string {
	s := strings.TrimSpace(raw)
	international := strings.HasPrefix(s, "+")

	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	d := b.String()

	switch {
	case d == "":
		return ""
	case international:
		return d
	case strings.HasPrefix(d, "00"):
		return d[2:]
	case strings.HasPrefix(d, "0"):
		return countryCode + d[1:]
	case countryCode != "" && strings.HasPrefix(d, countryCode) && len(d) > len(countryCode)+7:
		return d
	case countryCode != "" && len(d) <= 10:
		return countryCode + d
	default:
		return d
	}
}

// Mask hides all but the last four digits, for logging.
func Mask(number string) string {
	if len(number) <= 4 {
		return "****"
	}
	return strings.Repeat("*", len(number)-4) + number[len(number)-4:]
}

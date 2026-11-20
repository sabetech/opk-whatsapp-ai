// Package users looks up application users.
package users

import (
	"context"
	"fmt"

	"opk-whatsapp-ai/internal/phone"
	"opk-whatsapp-ai/internal/pocketbase"
)

// User is the subset of a PocketBase user needed to serve a request.
type User struct {
	ID    string
	Name  string
	Role  string
	Phone string
}

// Directory finds users in the PocketBase users collection.
type Directory struct {
	pb          *pocketbase.Client
	countryCode string
}

func NewDirectory(pb *pocketbase.Client, countryCode string) *Directory {
	return &Directory{pb: pb, countryCode: countryCode}
}

// ErrAmbiguous is returned when several users share the same phone number.
var ErrAmbiguous = fmt.Errorf("phone number matches more than one user")

// FindByPhone returns the user whose phone field matches number, comparing
// normalized international forms. It returns (nil, nil) if no user matches.
func (d *Directory) FindByPhone(ctx context.Context, number string) (*User, error) {
	want := phone.Normalize(number, d.countryCode)
	if len(want) < 9 {
		return nil, nil
	}

	// Narrow the search on the last 9 digits (digits only, safe to inline),
	// then compare normalized numbers exactly.
	suffix := want[len(want)-9:]

	res, err := d.pb.ListRecords(ctx, "users", pocketbase.ListOptions{
		Filter:  "phone ~ '" + suffix + "'",
		Fields:  "id,name,role,phone",
		PerPage: 20,
	})
	if err != nil {
		return nil, fmt.Errorf("look up user by phone: %w", err)
	}

	var match *User
	for _, rec := range res.Items {
		p, _ := rec["phone"].(string)
		if phone.Normalize(p, d.countryCode) != want {
			continue
		}
		if match != nil {
			return nil, ErrAmbiguous
		}
		match = &User{
			ID:    asString(rec["id"]),
			Name:  asString(rec["name"]),
			Role:  asString(rec["role"]),
			Phone: p,
		}
	}
	return match, nil
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

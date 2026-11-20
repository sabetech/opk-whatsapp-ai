package tools

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var collectionNamePattern = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// deniedCollections are never readable by the agent, even if allowlisted.
var deniedCollections = map[string]bool{
	"app_settings": true, // application configuration; may hold secrets
}

// CollectionAccess decides which PocketBase collections the agent may read.
// System collections (names starting with "_", e.g. _superusers) and
// deniedCollections are always denied. If an allowlist is configured, only
// those collections are allowed.
type CollectionAccess struct {
	allowed map[string]bool
}

func NewCollectionAccess(allowed []string) *CollectionAccess {
	access := &CollectionAccess{}
	if len(allowed) > 0 {
		access.allowed = make(map[string]bool, len(allowed))
		for _, name := range allowed {
			access.allowed[name] = true
		}
	}
	return access
}

func (a *CollectionAccess) Allowed(name string) bool {
	if !collectionNamePattern.MatchString(name) || strings.HasPrefix(name, "_") || deniedCollections[name] {
		return false
	}
	if a.allowed == nil {
		return true
	}
	return a.allowed[name]
}

func (a *CollectionAccess) Check(name string) error {
	if name == "" {
		return fmt.Errorf("collection is required")
	}
	if !a.Allowed(name) {
		return fmt.Errorf("access to collection %q is not permitted; call list_collections to see available collections", name)
	}
	return nil
}

func decodeArgs(raw json.RawMessage, out any) error {
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	return nil
}

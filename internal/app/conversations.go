package app

import "ai-pm-system/internal/domain"

// Old questions did not record the author. Preserve them for the project
// owner (or the local workspace owner), without guessing a personal identity.
func CanReadRun(a domain.Actor, p domain.Project, r domain.Run) bool {
	if !CanRead(a, p) {
		return false
	}
	if r.Kind != "question" {
		return true
	}
	if r.OwnerID != "" {
		return r.OwnerID == a.ID && r.OwnerKind == a.Kind
	}
	return a.Kind == "human" && (p.OwnerID == a.ID || (p.OwnerID == "" && a.ID == Human().ID))
}

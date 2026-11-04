package app

import "ai-pm-system/internal/domain"

// Personal OAuth and Web sessions represent the same employee. Local tokens
// have no UserID and retain their exact developer identity instead.
func OwnsRun(a domain.Actor, r domain.Run) bool {
	if r.OwnerID == "" || (r.OwnerKind != "human" && r.OwnerKind != "developer") {
		return false
	}
	if a.Kind == "developer" && a.UserID == "" {
		return r.OwnerKind == "developer" && r.OwnerID == a.ID
	}
	return (a.Kind == "human" || a.Kind == "developer") && actorUser(a) == r.OwnerID
}

// Old questions did not record the author. Preserve them for the project
// owner (or the local workspace owner), without guessing a personal identity.
func CanReadRun(a domain.Actor, p domain.Project, r domain.Run) bool {
	if !CanRead(a, p) {
		return false
	}
	if r.Kind != "question" {
		// User-triggered analysis can contain source available only to its owner.
		// Only background/legacy task runs are shared with the task pages.
		if r.OwnerID != "" {
			return OwnsRun(a, r) && CanAccessPage(a, p, "tasks")
		}
		return CanAccessAnyPage(a, p, "tasks", "reviews", "versions")
	}
	if !CanAccessPage(a, p, "chat") {
		return false
	}
	if r.OwnerID != "" {
		return OwnsRun(a, r)
	}
	return a.Kind == "human" && (p.OwnerID == a.ID || (p.OwnerID == "" && a.ID == Human().ID))
}

package app_test

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"testing"
)

func TestRunOwnerIsEmployeeAcrossWebAndOAuthButNotLocalToken(t *testing.T) {
	web := domain.Actor{ID: "employee", Kind: "human"}
	personal := domain.Actor{ID: "employee", UserID: "employee", Kind: "developer", ProjectID: "p"}
	other := domain.Actor{ID: "other", UserID: "other", Kind: "developer", ProjectID: "p"}
	legacy := domain.Actor{ID: "tok_local", Kind: "developer", ProjectID: "p"}
	for _, kind := range []string{"human", "developer"} {
		r := domain.Run{OwnerID: "employee", OwnerKind: kind}
		if !app.OwnsRun(web, r) || !app.OwnsRun(personal, r) || app.OwnsRun(other, r) || app.OwnsRun(legacy, r) {
			t.Fatal("employee and client owner boundaries diverged", kind)
		}
	}
	r := domain.Run{OwnerID: legacy.ID, OwnerKind: "developer"}
	if !app.OwnsRun(legacy, r) || app.OwnsRun(web, r) || app.OwnsRun(personal, r) {
		t.Fatal("local token borrowed an employee identity")
	}
}

package domain

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestProjectMemberReadsSingleRole(t *testing.T) {
	var p Project
	if e := json.Unmarshal([]byte(`{"memberships":[{"user_id":"a","role":"product"},{"user_id":"b","roles":["developer","tester"]}]}`), &p); e != nil {
		t.Fatal(e)
	}
	if !p.Memberships[0].Has("product") || len(p.Memberships[0].Roles) != 1 || !p.Memberships[1].Has("tester") {
		t.Fatal(p.Memberships)
	}
	b, _ := json.Marshal(p.Memberships[0])
	if string(b) != `{"user_id":"a","roles":["product"],"role":"product"}` {
		t.Fatal(string(b))
	}
}

func TestProjectMemberRetainsLegacyPermissions(t *testing.T) {
	for _, roles := range [][]string{{"tester"}, {"developer", "tester"}, {"product", "admin"}} {
		encoded, err := json.Marshal(ProjectMember{UserID: "member", Roles: roles})
		if err != nil {
			t.Fatal(err)
		}
		var old struct {
			UserID string `json:"user_id"`
			Role   string `json:"role"`
		}
		if err = json.Unmarshal(encoded, &old); err != nil {
			t.Fatal(err)
		}
		if old.Role == "" || (slices.Contains(roles, "admin") && old.Role != "admin") {
			t.Fatal(string(encoded))
		}
		var current ProjectMember
		if err = json.Unmarshal(encoded, &current); err != nil || !slices.Equal(current.Roles, roles) {
			t.Fatal(current, err)
		}
	}
}

package domain

import (
	"encoding/json"
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
	if string(b) != `{"user_id":"a","roles":["product"]}` {
		t.Fatal(string(b))
	}
}

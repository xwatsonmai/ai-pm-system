package domain

import (
	"encoding/json"
	"slices"
)

// ProjectRoles lists the roles a project member can hold, in display order.
// Roles decide who receives which to-dos; only admin grants extra permissions.
var ProjectRoles = []string{"product", "developer", "tester", "admin"}

type ProjectMember struct {
	UserID string   `json:"user_id"`
	Roles  []string `json:"roles"`
}

// UnmarshalJSON also reads the single role stored before members could hold several.
func (m *ProjectMember) UnmarshalJSON(b []byte) error {
	var v struct {
		UserID string   `json:"user_id"`
		Roles  []string `json:"roles"`
		Role   string   `json:"role"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	if v.Roles == nil && v.Role != "" {
		v.Roles = []string{v.Role}
	}
	*m = ProjectMember{UserID: v.UserID, Roles: v.Roles}
	return nil
}

// MarshalJSON retains a legacy role so existing clients and a rolled-back
// server can still read memberships. New readers use the complete role set.
func (m ProjectMember) MarshalJSON() ([]byte, error) {
	role := ""
	for _, candidate := range []string{"admin", "product", "developer"} {
		if m.Has(candidate) {
			role = candidate
			break
		}
	}
	if role == "" && len(m.Roles) > 0 {
		role = "developer"
	}
	return json.Marshal(struct {
		UserID string   `json:"user_id"`
		Roles  []string `json:"roles"`
		Role   string   `json:"role,omitempty"`
	}{m.UserID, m.Roles, role})
}

func (m ProjectMember) Has(role string) bool { return slices.Contains(m.Roles, role) }

// ProjectMembers lists memberships, deriving them for projects created
// before roles existed: the owner administers and other members develop.
func ProjectMembers(p Project) []ProjectMember {
	if len(p.Memberships) > 0 {
		return p.Memberships
	}
	out := []ProjectMember{}
	if p.OwnerID != "" {
		out = append(out, ProjectMember{UserID: p.OwnerID, Roles: []string{"admin"}})
	}
	for _, id := range p.AccessMembers {
		if id != p.OwnerID {
			out = append(out, ProjectMember{UserID: id, Roles: []string{"developer"}})
		}
	}
	return out
}

type PlatformSettings struct {
	ProfileID string `json:"profile_id"`
}

type DirectoryMember struct {
	User
	EmployeeID     int      `json:"employee_id"`
	DepartmentID   int      `json:"department_id"`
	DepartmentName string   `json:"department_name"`
	SubCompanyName string   `json:"subcompany_name"`
	JobTitle       string   `json:"job_title"`
	OrgPath        []string `json:"org_path"`
}

type OrganizationUnit struct {
	ID       int                `json:"id"`
	Type     string             `json:"type"`
	Name     string             `json:"name"`
	Children []OrganizationUnit `json:"children"`
}

type DirectorySnapshot struct {
	Members  []DirectoryMember  `json:"members"`
	Roots    []OrganizationUnit `json:"roots"`
	SyncedAt int64              `json:"synced_at"`
}

type MCPAccount struct {
	UserID     string `json:"user_id"`
	Enabled    bool   `json:"enabled"`
	Generation int64  `json:"generation"`
}

type OAuthClient struct {
	ID           string   `json:"client_id"`
	Name         string   `json:"client_name"`
	RedirectURIs []string `json:"redirect_uris"`
}

type OAuthCode struct {
	Hash, ClientID, RedirectURI, Challenge, Resource string
	UserID, UserName, ProjectID                      string
	ExpiresAt                                        int64
	AccountGeneration                                int64
}

type OAuthGrant struct {
	ID                      string `json:"id"`
	UserID                  string `json:"user_id"`
	UserName                string `json:"-"`
	ClientID                string `json:"client_id"`
	ProjectID               string `json:"project_id"`
	Resource                string `json:"-"`
	AccessHash, RefreshHash string `json:"-"`
	AccessExpiresAt         int64  `json:"expires_at"`
	RefreshExpiresAt        int64  `json:"-"`
	// The refresh token this grant last rotated away from stays redeemable
	// until PreviousRefreshUntil, so a client whose refresh response was lost
	// can retry instead of signing in again.
	PreviousRefreshHash  string `json:"-"`
	PreviousRefreshUntil int64  `json:"-"`
	CreatedAt            int64  `json:"created_at"`
	AccountGeneration    int64  `json:"-"`
}

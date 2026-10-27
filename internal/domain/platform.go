package domain

type ProjectMember struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
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

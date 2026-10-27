package server

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
)

func (s *Server) platformRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/agent/status", func(w http.ResponseWriter, r *http.Request) {
		p, e := s.App.Store.GetPlatformSettings(r.Context())
		if e != nil {
			fail(w, e)
			return
		}
		out := map[string]any{"profile_id": p.ProfileID, "configured": p.ProfileID != ""}
		if p.ProfileID != "" {
			v, e := s.App.Store.GetProfile(r.Context(), p.ProfileID)
			if e != nil {
				fail(w, e)
				return
			}
			out["name"] = v.Name
			out["model"] = v.Model
		}
		respond(w, out)
	})
	mux.HandleFunc("GET /api/platform/settings", s.onlyAdmin(func(w http.ResponseWriter, r *http.Request) {
		p, e := s.App.Store.GetPlatformSettings(r.Context())
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, p)
	}))
	mux.HandleFunc("PUT /api/platform/settings", s.onlyAdmin(func(w http.ResponseWriter, r *http.Request) {
		var p domain.PlatformSettings
		if e := read(r, &p); e != nil {
			fail(w, e)
			return
		}
		if p.ProfileID != "" {
			if _, e := s.App.Store.GetProfile(r.Context(), p.ProfileID); e != nil {
				fail(w, e)
				return
			}
		}
		if e := s.App.Store.PutPlatformSettings(r.Context(), p); e != nil {
			fail(w, e)
			return
		}
		respond(w, p)
	}))
	mux.HandleFunc("GET /api/platform/projects", s.onlyAdmin(func(w http.ResponseWriter, r *http.Request) {
		p, e := s.App.Store.ListProjects(r.Context())
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, p)
	}))
	mux.HandleFunc("GET /api/platform/admins", s.onlyAdmin(func(w http.ResponseWriter, r *http.Request) {
		ids, e := s.App.Store.ListPlatformAdmins(r.Context())
		if e != nil {
			fail(w, e)
			return
		}
		users, e := s.App.Store.ListUsers(r.Context())
		if e != nil {
			fail(w, e)
			return
		}
		out := []domain.User{}
		for _, id := range ids {
			u := domain.User{ID: id, Name: id}
			for _, v := range users {
				if v.ID == id {
					u = v
				}
			}
			out = append(out, u)
		}
		if s.Auth == nil {
			out = append(out, domain.User{ID: "local-owner", Name: "本地管理员"})
		}
		respond(w, out)
	}))
	mux.HandleFunc("PUT /api/platform/admins/{user}", s.onlyAdmin(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Enabled bool `json:"enabled"`
		}
		if e := read(r, &in); e != nil {
			fail(w, e)
			return
		}
		if in.Enabled {
			if e := s.listed(r.Context(), r.PathValue("user")); e != nil {
				fail(w, e)
				return
			}
		}
		users, e := s.App.Store.ListUsers(r.Context())
		if e != nil {
			fail(w, e)
			return
		}
		i := slices.IndexFunc(users, func(u domain.User) bool { return u.ID == r.PathValue("user") })
		if i < 0 {
			fail(w, domain.ErrNotFound)
			return
		}
		if !in.Enabled && s.Auth != nil && s.Auth.BootstrapAdmin(users[i].LoginID) {
			fail(w, errors.New("首批管理员由服务器 platform_admin_login_ids 配置管理"))
			return
		}
		if e := s.App.Store.SetPlatformAdmin(r.Context(), users[i].ID, in.Enabled); e != nil {
			fail(w, e)
			return
		}
		respond(w, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("GET /api/platform/directory", s.onlyAdmin(s.directory))
	mux.HandleFunc("POST /api/platform/directory/sync", s.onlyAdmin(s.directory))
	mux.Handle("GET /api/projects/{id}/directory", s.projectAccess(http.HandlerFunc(s.directory)))
	mux.Handle("POST /api/projects/{id}/directory/sync", s.projectAccess(http.HandlerFunc(s.directory)))
	mux.Handle("GET /api/projects/{id}/members", s.projectAccess(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, e := s.App.Store.GetProject(r.Context(), r.PathValue("id"))
		if e != nil {
			fail(w, e)
			return
		}
		users, e := s.App.Store.ListUsers(r.Context())
		if e != nil {
			fail(w, e)
			return
		}
		memberships := p.Memberships
		if len(memberships) == 0 {
			if p.OwnerID != "" {
				memberships = append(memberships, domain.ProjectMember{UserID: p.OwnerID, Role: "admin"})
			}
			for _, id := range p.AccessMembers {
				if id != p.OwnerID {
					memberships = append(memberships, domain.ProjectMember{UserID: id, Role: "developer"})
				}
			}
		}
		out := []map[string]any{}
		snapshot, _ := s.App.Store.GetDirectory(r.Context())
		for _, m := range memberships {
			u := domain.User{ID: m.UserID, Name: m.UserID}
			for _, v := range users {
				if v.ID == m.UserID {
					u = v
				}
			}
			if m.UserID == "local-owner" {
				u.Name = "本地管理员"
			}
			department := ""
			for _, v := range snapshot.Members {
				if v.ID == m.UserID {
					department = v.DepartmentName
				}
			}
			out = append(out, map[string]any{"user": u, "role": m.Role, "department_name": department})
		}
		respond(w, map[string]any{"items": out, "can_manage": app.CanManage(s.actor(r), p)})
	})))
	member := func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Role string `json:"role"`
		}
		if r.Method != "DELETE" {
			if e := read(r, &in); e != nil {
				fail(w, e)
				return
			}
		}
		if r.Method != "DELETE" {
			if e := s.listed(r.Context(), r.PathValue("user")); e != nil {
				fail(w, e)
				return
			}
		}
		p, e := s.App.SetProjectMember(r.Context(), s.actor(r), r.PathValue("id"), r.PathValue("user"), in.Role, r.Method == "DELETE")
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, p)
	}
	mux.Handle("PUT /api/projects/{id}/members/{user}", s.projectAccess(http.HandlerFunc(member)))
	mux.Handle("DELETE /api/projects/{id}/members/{user}", s.projectAccess(http.HandlerFunc(member)))
}

func (s *Server) directory(w http.ResponseWriter, r *http.Request) {
	var snapshot domain.DirectorySnapshot
	var e error
	label := "平台用户"
	if s.Auth != nil && s.Auth.Directory != nil {
		label = s.Auth.Directory.DirectoryLabel()
		snapshot, e = s.Auth.Directory.SyncDirectory(r.Context(), r.Method == "POST")
	} else {
		snapshot, e = s.App.Store.GetDirectory(r.Context())
		if errors.Is(e, domain.ErrNotFound) {
			e = nil
			users, err := s.App.Store.ListUsers(r.Context())
			if err != nil {
				fail(w, err)
				return
			}
			snapshot = domain.DirectorySnapshot{Members: []domain.DirectoryMember{}, Roots: []domain.OrganizationUnit{}}
			if s.Auth == nil {
				snapshot.Members = append(snapshot.Members, domain.DirectoryMember{User: domain.User{ID: "local-owner", Name: "本地管理员"}})
			}
			for _, u := range users {
				snapshot.Members = append(snapshot.Members, domain.DirectoryMember{User: u})
			}
		}
	}
	if e != nil {
		old, _ := s.App.Store.GetDirectory(r.Context())
		respondStatus(w, http.StatusServiceUnavailable, map[string]any{"error": e.Error(), "synced_at": old.SyncedAt})
		return
	}
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	dept, _ := strconv.Atoi(r.URL.Query().Get("department_id"))
	page := number(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	matches := []domain.DirectoryMember{}
	for _, u := range snapshot.Members {
		if dept != 0 && u.DepartmentID != dept {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(u.Name+" "+u.LoginID+" "+u.DepartmentName+" "+u.SubCompanyName+" "+u.JobTitle+" "+strings.Join(u.OrgPath, " ")), query) {
			continue
		}
		matches = append(matches, u)
	}
	start := min((page-1)*50, len(matches))
	end := min(start+50, len(matches))
	respond(w, map[string]any{"items": matches[start:end], "total": len(matches), "page": page, "page_size": 50, "roots": snapshot.Roots, "synced_at": snapshot.SyncedAt, "label": label})
}

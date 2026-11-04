package server

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/credential"
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/gitrepo"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
)

type repositoryProject struct {
	domain.Project
	Syncing bool `json:"syncing"`
}

func (s *Server) repositoryProject(ctx context.Context, p domain.Project) repositoryProject {
	syncing := s.Git != nil && s.Git.Syncing(p.ID)
	// Read the receipt after checking the lock so completion cannot leave the
	// response with both an old empty SHA and syncing=false.
	if latest, e := s.App.Store.GetProject(ctx, p.ID); e == nil {
		p.SHA, p.SyncedAt, p.SyncError = latest.SHA, latest.SyncedAt, latest.SyncError
	}
	return repositoryProject{Project: p, Syncing: syncing}
}

type repositoryInput struct {
	Repository  string            `json:"repository"`
	ProjectID   string            `json:"project_id"`
	Auth        credential.Secret `json:"repository_auth"`
	GeneratedID string            `json:"generated_credential_id"`
}

func (s *Server) repositorySecret(r *http.Request, in repositoryInput) (credential.Secret, string, error) {
	a := s.actor(r)
	secret := in.Auth
	old := domain.Project{}
	if in.ProjectID != "" {
		var e error
		old, e = s.App.Store.GetProject(r.Context(), in.ProjectID)
		if e != nil {
			return secret, "", e
		}
		if !app.CanAccessPage(a, old, "git") {
			return secret, "", domain.ErrForbidden
		}
	}
	if secret.Mode == "" {
		secret.Mode = old.AuthMode
		if secret.Mode == "" {
			if old.ID != "" {
				secret.Mode = "server"
			} else {
				secret.Mode = "none"
			}
		}
	}
	legacyServer := old.ID != "" && in.Repository == old.Repository && (old.AuthMode == "" || old.AuthMode == "server")
	allowLocal := a.ID == "local-owner" || legacyServer
	if secret.Mode == "server" && !allowLocal {
		return secret, "", errors.New("登录模式下新项目请配置专用 Token 或 SSH 密钥；服务器已有凭据仅兼容原项目")
	}
	if e := gitrepo.ValidateRepository(in.Repository, secret.Mode, allowLocal); e != nil {
		return secret, "", e
	}
	if in.Repository == "" && secret.Mode != "none" && secret.Mode != "server" {
		return secret, "", errors.New("请先填写仓库地址")
	}
	id := old.CredentialID
	if in.GeneratedID != "" {
		id = in.GeneratedID
	}
	var previous credential.Secret
	if id != "" && (secret.Mode == "token" || secret.Mode == "ssh") {
		if s.Git == nil || s.Git.Vault == nil {
			return secret, "", errors.New("仓库凭据存储未初始化")
		}
		saved, owner, e := s.Git.Vault.Load(r.Context(), id)
		if e != nil {
			return secret, "", e
		}
		if id != old.CredentialID && owner != a.ID {
			return secret, "", domain.ErrForbidden
		}
		if saved.Mode == secret.Mode {
			previous = saved
			if secret.Username == "" {
				secret.Username = saved.Username
			}
			if secret.Token == "" {
				secret.Token = saved.Token
			}
			if secret.PrivateKey == "" {
				secret.PrivateKey = saved.PrivateKey
			}
			if secret.KnownHosts == "" {
				secret.KnownHosts = saved.KnownHosts
			}
		}
	}
	normalized, e := gitrepo.NormalizeSecret(secret)
	if e != nil {
		return secret, "", e
	}
	if !reflect.DeepEqual(previous, normalized) {
		id = ""
	}
	return normalized, id, nil
}
func (s *Server) saveProject(w http.ResponseWriter, r *http.Request) {
	var in struct {
		domain.Project
		Auth        credential.Secret `json:"repository_auth"`
		GeneratedID string            `json:"generated_credential_id"`
	}
	var raw json.RawMessage
	if e := read(r, &raw); e != nil {
		fail(w, e)
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if e := decoder.Decode(&in); e != nil {
		fail(w, e)
		return
	}
	fields := map[string]json.RawMessage{}
	_ = json.Unmarshal(raw, &fields)
	in.ID = r.PathValue("id")
	if in.ID != "" {
		old, e := s.App.Store.GetProject(r.Context(), in.ID)
		if e != nil {
			fail(w, e)
			return
		}
		// Legacy PUT clients may send only one page's fields. Missing fields retain
		// their stored values and cannot implicitly clear another page's settings.
		for key, target := range map[string]*string{"name": &in.Name, "description": &in.Description, "repository": &in.Repository, "branch": &in.Branch, "agent_read_mode": &in.AgentReadMode, "agent_ref": &in.AgentRef, "auth_mode": &in.AuthMode, "credential_id": &in.CredentialID} {
			if _, ok := fields[key]; !ok {
				switch key {
				case "name":
					*target = old.Name
				case "description":
					*target = old.Description
				case "repository":
					*target = old.Repository
				case "branch":
					*target = old.Branch
				case "agent_read_mode":
					*target = old.AgentReadMode
				case "agent_ref":
					*target = old.AgentRef
				case "auth_mode":
					*target = old.AuthMode
				case "credential_id":
					*target = old.CredentialID
				}
			}
		}
		if (in.Name != old.Name || in.Description != old.Description) && !app.CanAccessPage(s.actor(r), old, "settings") {
			fail(w, domain.ErrForbidden)
			return
		}
		_, hasAuth := fields["repository_auth"]
		changed := hasAuth || in.GeneratedID != "" || in.Repository != old.Repository || in.Branch != old.Branch || in.AgentReadMode != old.AgentReadMode || in.AgentRef != old.AgentRef || in.AuthMode != old.AuthMode || in.CredentialID != old.CredentialID
		if !changed {
			p, e := s.App.SaveProjectAs(r.Context(), s.actor(r), in.Project)
			if e != nil {
				fail(w, e)
				return
			}
			respond(w, s.repositoryProjectFor(r, p))
			return
		}
		if !app.CanAccessPage(s.actor(r), old, "git") {
			fail(w, domain.ErrForbidden)
			return
		}
	}

	secret, id, e := s.repositorySecret(r, repositoryInput{Repository: in.Repository, ProjectID: in.ID, Auth: in.Auth, GeneratedID: in.GeneratedID})
	if e != nil {
		fail(w, e)
		return
	}
	if secret.Mode == "ssh" || secret.Mode == "token" {
		if s.Git == nil || s.Git.Vault == nil {
			fail(w, errors.New("仓库凭据存储未初始化"))
			return
		}
		if id == "" {
			id, e = s.Git.Vault.Save(r.Context(), s.actor(r).ID, secret)
			if e != nil {
				fail(w, e)
				return
			}
		}
	} else {
		id = ""
	}
	in.Project.AuthMode = secret.Mode
	in.Project.CredentialID = id
	p, e := s.App.SaveProjectAs(r.Context(), s.actor(r), in.Project)
	if e != nil {
		fail(w, e)
		return
	}
	if p.Repository != "" && p.SyncedAt == 0 && s.Git != nil {
		s.Git.SyncAsync(r.Context(), p.ID)
	}
	respond(w, s.repositoryProjectFor(r, p))
}
func (s *Server) repositoryRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/projects/{id}/repository-status", s.projectAccess(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, e := s.App.Store.GetProject(r.Context(), r.PathValue("id"))
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, s.repositoryProjectFor(r, p))
	})))
	mux.HandleFunc("GET /api/users", s.onlyAdmin(func(w http.ResponseWriter, r *http.Request) {
		users, e := s.App.Store.ListUsers(r.Context())
		if e != nil {
			fail(w, e)
			return
		}
		if s.Auth == nil {
			users = append(users, domain.User{ID: "local-owner", Name: "项目负责人"})
		}
		respond(w, users)
	}))
	mux.HandleFunc("POST /api/repository/test", func(w http.ResponseWriter, r *http.Request) {
		var in repositoryInput
		if e := read(r, &in); e != nil {
			fail(w, e)
			return
		}
		secret, _, e := s.repositorySecret(r, in)
		if e != nil {
			fail(w, e)
			return
		}
		if in.Repository == "" {
			fail(w, errors.New("请先填写仓库地址"))
			return
		}
		if s.Git == nil {
			fail(w, errors.New("Git 服务未初始化"))
			return
		}
		result, e := s.Git.Test(r.Context(), in.Repository, secret)
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, result)
	})
	mux.HandleFunc("POST /api/repository/ssh-key", func(w http.ResponseWriter, r *http.Request) {
		if s.Git == nil {
			fail(w, errors.New("Git 服务未初始化"))
			return
		}
		result, e := s.Git.GenerateKey(r.Context(), s.actor(r).ID)
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, result)
	})
	mux.Handle("GET /api/projects/{id}/repository-auth", s.projectAccess(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, e := s.App.Store.GetProject(r.Context(), r.PathValue("id"))
		if e != nil {
			fail(w, e)
			return
		}
		out := map[string]any{"mode": p.AuthMode, "configured": p.CredentialID != ""}
		if p.CredentialID != "" {
			if s.Git == nil || s.Git.Vault == nil {
				fail(w, errors.New("仓库凭据存储未初始化"))
				return
			}
			secret, _, e := s.Git.Vault.Load(r.Context(), p.CredentialID)
			if e != nil {
				fail(w, e)
				return
			}
			out["username"] = secret.Username
			out["public_key"] = secret.PublicKey
			out["known_hosts"] = secret.KnownHosts
		}
		respond(w, out)
	})))
}

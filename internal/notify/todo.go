package notify

import (
	"ai-pm-system/internal/domain"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strconv"
	"strings"
)

// Todo is work waiting on someone, derived from a task's current state. Its
// ID stays the same while the same work waits, so deriving again is harmless.
type Todo struct {
	ID string `json:"id"`
	// Base is the derived ID. When the same work waits again after it closed,
	// such as a task returned for claiming, the new to-do gets a suffix.
	Base       string   `json:"base"`
	TaskID     string   `json:"task_id"`
	ProjectID  string   `json:"project_id"`
	Kind       string   `json:"kind"`
	Title      string   `json:"title"`
	Subject    string   `json:"subject"`
	Project    string   `json:"project"`
	Fields     []Field  `json:"fields,omitempty"`
	Recipients []string `json:"recipients"`
	// Action is the one step a card button may take, such as "test_pass";
	// anything that needs input or a choice opens the to-do page instead.
	Action    string `json:"action,omitempty"`
	Open      bool   `json:"open"`
	Done      string `json:"done,omitempty"`
	CreatedAt int64  `json:"created_at"`
	ClosedAt  int64  `json:"closed_at,omitempty"`
}

// Kinds lists the to-dos in the order the to-do page explains them.
var Kinds = map[string]string{
	"review_reply":    "需求评审等你回复",
	"review_confirm":  "需求稿待确认",
	"bug_rejection":   "评审建议驳回 Bug，待你确认",
	"alignment_reply": "开发对齐等你回复",
	"claim":           "有需求待认领",
	"test":            "待测试",
	"retest":          "Bug 已处理完，待复测",
	"verify":          "Bug 修复待验证",
	"release":         "测试通过，待上线",
	"acceptance":      "已上线，待验收",
}

// ActionLabels names the card button for each one-step action.
var ActionLabels = map[string]string{
	"confirm":           "确认需求稿",
	"confirm_rejection": "同意驳回",
	"test_pass":         "测试通过",
	"verify_bug":        "验证通过",
	"accept":            "验收通过",
}

func short(sha string) string { return sha[:min(len(sha), 8)] }

// Derive lists the to-dos a task has right now. review may be nil.
func Derive(t domain.Task, review *domain.RequirementReview, p domain.Project) []Todo {
	if !domain.TaskActive(t.Status) {
		return nil
	}
	r := recipients{t: t, members: domain.ProjectMembers(p)}
	out := []Todo{}
	add := func(kind, key string, to []string, action string, fields ...Field) {
		if len(to) == 0 {
			return
		}
		sum := sha256.Sum256([]byte(t.ID + "\x00" + kind + "\x00" + key))
		id := "td_" + hex.EncodeToString(sum[:12])
		out = append(out, Todo{ID: id, Base: id, TaskID: t.ID, ProjectID: t.ProjectID, Kind: kind, Title: Kinds[kind], Subject: t.Title, Project: p.Name, Fields: fields, Recipients: to, Action: action, Open: true})
	}
	kind := Field{"类型", map[bool]string{true: "Bug", false: "需求"}[t.Kind == "bug"]}
	if review != nil && review.TaskID == t.ID && t.Status == "clarify" {
		// A new Agent round or form is new work, even under the same status.
		last := ""
		for _, m := range review.Messages {
			if m.Role == "assistant" && m.Generation == review.Generation {
				last = m.ID
			}
		}
		key := strconv.Itoa(review.Generation) + ":" + last
		switch review.Status {
		case "needs_input":
			add("review_reply", key, r.owner("product"), "", kind)
		case "ready_to_confirm":
			action := "confirm"
			if review.NeedsRecheck {
				action = ""
			}
			add("review_confirm", key, r.owner("product"), action, kind)
		case "rejection_pending":
			add("bug_rejection", key, r.owner("product"), "confirm_rejection", kind)
		}
	}
	if t.Alignment != nil && t.Alignment.Status == "waiting_product" {
		key := ""
		if review != nil {
			key = strconv.Itoa(review.Generation) + ":" + strconv.Itoa(len(review.Messages))
		}
		add("alignment_reply", key, r.owner("product"), "", kind)
	}
	sub := t.Submission
	switch t.Status {
	case "ready":
		if t.Claimant == "" {
			add("claim", strconv.Itoa(t.Revision), r.role("developer"), "", kind)
		}
	case "testing":
		if sub != nil {
			key := strconv.Itoa(t.Revision) + ":" + sub.SHA + ":" + sub.Environment
			add("test", key, r.role("tester"), map[bool]string{true: "test_pass"}[t.ParentRequirementID == ""], kind, Field{"版本", short(sub.SHA)}, Field{"环境", sub.Environment})
		}
	case "retest":
		add("retest", strconv.Itoa(t.Revision), r.role("tester"), "", kind)
	case "verifying":
		if sub != nil {
			key := strconv.Itoa(t.Revision) + ":" + sub.SHA + ":" + sub.Environment
			add("verify", key, r.role("tester"), map[bool]string{true: "verify_bug"}[t.ParentRequirementID != ""], kind, Field{"版本", short(sub.SHA)}, Field{"环境", sub.Environment})
		}
	case "pending_release":
		if sub != nil {
			add("release", strconv.Itoa(t.Revision)+":"+sub.SHA, r.claimant("developer"), "", kind, Field{"版本", short(sub.SHA)})
		}
	case "review":
		if d := t.Deployment; d != nil && sub != nil && d.SHA == sub.SHA && d.Revision == t.Revision {
			action := ""
			if len(t.Acceptance) > 0 {
				action = "accept"
			}
			add("acceptance", strconv.Itoa(t.Revision)+":"+d.SHA+":"+d.Environment, r.owner("product"), action, kind, Field{"环境", d.Environment}, Field{"验收条件", strconv.Itoa(len(t.Acceptance)) + " 条"})
		}
	}
	return out
}

type recipients struct {
	t       domain.Task
	members []domain.ProjectMember
}

// role lists members with a role. Testing falls back to product, who tested
// before the tester role existed and still do in teams without testers.
func (r recipients) role(role string) []string {
	out := []string{}
	for _, m := range r.members {
		if m.Has(role) && m.UserID != "local-owner" {
			out = append(out, m.UserID)
		}
	}
	if len(out) == 0 && role == "tester" {
		return r.role("product")
	}
	return out
}

func (r recipients) member(id string) bool {
	return id != "" && id != "local-owner" && slices.ContainsFunc(r.members, func(m domain.ProjectMember) bool { return m.UserID == id })
}

// owner prefers whoever raised the task, then everyone with the role.
func (r recipients) owner(role string) []string {
	if c := r.t.Creator; c != nil && r.member(c.ID) {
		return []string{c.ID}
	}
	return r.role(role)
}

// claimant prefers the person whose personal MCP client claimed the task.
func (r recipients) claimant(role string) []string {
	if id := strings.TrimSpace(r.t.Claimant); r.member(id) {
		return []string{id}
	}
	return r.role(role)
}

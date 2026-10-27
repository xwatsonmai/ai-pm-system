package app

import (
	"ai-pm-system/internal/domain"
	"encoding/json"
	"path"
)

// ReviewStepLabel describes a review tool call in words a product reader
// recognizes. It names what is being read, never internal identifiers.
func ReviewStepLabel(name string, args json.RawMessage) string {
	var in struct {
		Action string `json:"action"`
		Path   string `json:"path"`
	}
	_ = json.Unmarshal(args, &in)
	file := ""
	if in.Path != "" {
		file = "（" + path.Base(in.Path) + "）"
	}
	switch name {
	case "requirement_pool":
		return "查找相关需求"
	case "requirement_detail":
		return "阅读相关需求"
	case "requirement_iterations":
		return "查看历次迭代"
	case "project_context":
		return "查看项目资料"
	case "project_milestone_get", "project_timeline", "project_version_list", "project_version_get_context":
		return "查看项目进度"
	case "repository_info", "repository_history":
		return "查看代码仓库"
	case "repository_read":
		switch in.Action {
		case "file":
			return "阅读代码" + file
		case "search":
			return "搜索代码"
		case "tree":
			return "浏览代码目录"
		case "diff", "log":
			return "查看代码改动"
		}
		return "阅读代码"
	case "read":
		return "阅读代码" + file
	case "grep":
		return "搜索代码"
	case "find", "ls":
		return "浏览代码目录"
	case "submit_requirement_review":
		return "整理回复"
	}
	return "查阅资料"
}

// ReviewReadLabel names the requirement the Agent actually opened.
func ReviewReadLabel(title string, own bool) string {
	if own {
		return "回看本需求的记录"
	}
	return "阅读「" + title + "」"
}

// ReviewProgress lists the latest steps of a running review round.
func ReviewProgress(r domain.Run) []domain.RequirementReviewProgress {
	steps := r.Steps
	if len(steps) > 8 {
		steps = steps[len(steps)-8:]
	}
	out := make([]domain.RequirementReviewProgress, 0, len(steps))
	for _, s := range steps {
		label := s.Label
		if label == "" {
			label = ReviewStepLabel(s.Name, nil)
		}
		out = append(out, domain.RequirementReviewProgress{Label: label, Status: s.Status, At: s.At})
	}
	return out
}

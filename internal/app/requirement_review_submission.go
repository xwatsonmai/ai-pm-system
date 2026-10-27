package app

import (
	"ai-pm-system/internal/domain"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"unicode/utf8"
)

// RequirementReviewSubmission is the platform Agent's output for one review
// round. It maps onto the stored result, so answer links, question notes and
// form expiry keep working for every round.
type RequirementReviewSubmission struct {
	Reply         string                       `json:"reply"`
	Analysis      string                       `json:"analysis"`
	Decision      string                       `json:"decision"`
	Agenda        []domain.RequirementTopic    `json:"agenda"`
	Ask           *RequirementAskInput         `json:"ask"`
	Relations     []domain.RequirementRelation `json:"relations"`
	Proposal      *domain.RequirementProposal  `json:"proposal"`
	ExpireFormIDs []string                     `json:"expire_form_ids"`
}

type RequirementAskInput struct {
	TopicID      string                       `json:"topic_id"`
	Kind         string                       `json:"kind"`
	Question     string                       `json:"question"`
	QuickReplies []string                     `json:"quick_replies"`
	Questions    []domain.RequirementQuestion `json:"questions"`
}

func (in RequirementReviewSubmission) Result() domain.RequirementReviewResult {
	out := domain.RequirementReviewResult{Body: in.Reply, Analysis: in.Analysis, Decision: in.Decision, Agenda: in.Agenda, Relations: in.Relations, Proposal: in.Proposal, ExpireFormIDs: in.ExpireFormIDs, Questions: []domain.RequirementQuestion{}}
	if out.Agenda == nil {
		out.Agenda = []domain.RequirementTopic{}
	}
	if in.Ask != nil {
		out.Ask = &domain.RequirementAsk{Kind: in.Ask.Kind, TopicID: in.Ask.TopicID}
		if in.Ask.Kind == "direction" {
			out.Ask.Question, out.Ask.QuickReplies = in.Ask.Question, in.Ask.QuickReplies
		} else {
			out.Questions = in.Ask.Questions
		}
	}
	return out
}

// Generated record IDs (prefix + 24 hex characters) must never reach product readers.
var internalID = regexp.MustCompile(`\b[a-z]+_[0-9a-f]{24}\b`)

const (
	maxReplyRunes      = 400
	maxAnalysisRunes   = 20000
	maxTopicTitleRunes = 20
	maxConclusionRunes = 100
	maxQuestionRunes   = 200
	maxQuickReplyRunes = 30
	maxOpenTopics      = 6
	maxTopics          = 20
	maxFormQuestions   = 3
)

// ValidateRequirementReviewSubmission enforces the conversation rules when the
// tool is called, so the model corrects itself within the same round. previous
// is the stored agenda: topics may change status but never silently disappear.
func ValidateRequirementReviewSubmission(in RequirementReviewSubmission, previous []domain.RequirementTopic) error {
	if !nonempty(in.Reply) {
		return errors.New("reply 不能为空：用一条群聊消息说明本轮确认了什么，再引出问题")
	}
	if n := utf8.RuneCountInString(in.Reply); n > maxReplyRunes {
		return fmt.Errorf("reply 有 %d 字，不能超过 %d 字；较长的推理和依据放进 analysis", n, maxReplyRunes)
	}
	if utf8.RuneCountInString(in.Analysis) > maxAnalysisRunes {
		return fmt.Errorf("analysis 不能超过 %d 字", maxAnalysisRunes)
	}
	topics := map[string]domain.RequirementTopic{}
	open, openDirection := 0, false
	if len(in.Agenda) > maxTopics {
		return fmt.Errorf("agenda 最多 %d 个话题", maxTopics)
	}
	for _, t := range in.Agenda {
		if !nonempty(t.ID) || topics[t.ID].ID != "" {
			return errors.New("agenda 中每个话题需要唯一 id")
		}
		topics[t.ID] = t
		if !nonempty(t.Title) || utf8.RuneCountInString(t.Title) > maxTopicTitleRunes {
			return fmt.Errorf("话题标题不能为空，且不超过 %d 字", maxTopicTitleRunes)
		}
		if t.Kind != "direction" && t.Kind != "detail" {
			return errors.New("话题 kind 只能是 direction（方向性）或 detail（细节）")
		}
		switch t.Status {
		case "open":
			open++
			openDirection = openDirection || t.Kind == "direction"
		case "resolved":
			if !nonempty(t.Conclusion) {
				return fmt.Errorf("已确认的话题「%s」需要用一句话写 conclusion", t.Title)
			}
		case "dropped":
		default:
			return errors.New("话题 status 只能是 open、resolved 或 dropped")
		}
		if utf8.RuneCountInString(t.Conclusion) > maxConclusionRunes {
			return fmt.Errorf("话题结论不超过 %d 字", maxConclusionRunes)
		}
	}
	if open > maxOpenTopics {
		return fmt.Errorf("同时待确认的话题最多 %d 个，请合并或推迟次要话题", maxOpenTopics)
	}
	for _, t := range previous {
		if _, ok := topics[t.ID]; !ok {
			return fmt.Errorf("agenda 缺少已有话题「%s」(%s)：不再需要时请标为 dropped，不能直接删除", t.Title, t.ID)
		}
	}
	visible := []string{in.Reply, in.Analysis}
	for _, t := range in.Agenda {
		visible = append(visible, t.Title, t.Conclusion)
	}
	if a := in.Ask; a != nil {
		topic, ok := topics[a.TopicID]
		if !ok || topic.Status != "open" {
			return errors.New("ask.topic_id 必须指向 agenda 中 status=open 的话题")
		}
		if openDirection && topic.Kind != "direction" {
			return errors.New("还有未确认的方向性话题：先用 kind=direction 问方向性话题，方向确定后再问细节")
		}
		switch a.Kind {
		case "direction":
			if !nonempty(a.Question) || utf8.RuneCountInString(a.Question) > maxQuestionRunes {
				return fmt.Errorf("方向性提问需要一句话 question，不超过 %d 字", maxQuestionRunes)
			}
			if len(a.Questions) > 0 {
				return errors.New("方向性问题直接用文字问，不能附表单 questions")
			}
			if len(a.QuickReplies) > 3 {
				return errors.New("quick_replies 最多 3 条")
			}
			for i, reply := range a.QuickReplies {
				if !nonempty(reply) || utf8.RuneCountInString(reply) > maxQuickReplyRunes || slices.Contains(a.QuickReplies[:i], reply) {
					return fmt.Errorf("每条快捷回复不能为空、不能重复，且不超过 %d 字", maxQuickReplyRunes)
				}
			}
		case "form":
			if openDirection {
				return errors.New("方向性问题直接用文字问（kind=direction），不出表单")
			}
			if len(a.Questions) == 0 || len(a.Questions) > maxFormQuestions {
				return fmt.Errorf("一张表单只谈一个话题，需要 1 至 %d 题", maxFormQuestions)
			}
			if len(a.QuickReplies) > 0 {
				return errors.New("快捷回复只用于方向性提问")
			}
		default:
			return errors.New("ask.kind 只能是 direction 或 form")
		}
		visible = append(visible, a.Question)
		visible = append(visible, a.QuickReplies...)
		for _, q := range a.Questions {
			visible = append(visible, q.Label, q.Reason)
			visible = append(visible, q.Options...)
			visible = append(visible, q.Recommended...)
		}
	}
	switch in.Decision {
	case "clarify":
		if in.Ask == nil {
			return errors.New("decision=clarify 时必须带 ask：每轮问一个话题；解释含义时也要用更简单的说法重新问")
		}
	case "ready":
		if in.Ask != nil || open > 0 {
			return errors.New("decision=ready 时所有话题都应已 resolved 或 dropped，且不带 ask")
		}
	case "reject_bug":
		if in.Ask != nil {
			return errors.New("驳回建议不能同时提问")
		}
	}
	for _, text := range visible {
		if internalID.MatchString(text) {
			return errors.New("面向产品的文字里不能出现内部编号（如 rm_、tsk_ 开头的编号），请用需求标题或「上一轮的问题」等说法")
		}
	}
	return ValidateRequirementReviewResult(in.Result())
}

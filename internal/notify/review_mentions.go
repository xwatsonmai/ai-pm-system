package notify

import (
	"ai-pm-system/internal/domain"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"slices"
	"strings"
	"time"
)

// ReviewMentionNotice is recovered from committed messages after a restart.
// It does not create work in the recipient's to-do list.
type ReviewMentionNotice struct {
	TaskID    string `json:"task_id"`
	ProjectID string `json:"project_id"`
	MessageID string `json:"message_id"`
	Subject   string `json:"subject"`
	Project   string `json:"project"`
	Sender    string `json:"sender"`
	Preview   string `json:"preview"`
}

func mentionDeliveryID(message, user, channel string) string {
	sum := sha256.Sum256([]byte("review-mention\x00" + message + "\x00" + user + "\x00" + channel))
	return "dl_" + hex.EncodeToString(sum[:12])
}

func (h *Hub) reconcileMentions(ctx context.Context, t domain.Task) error {
	v, err := h.Data.GetRequirementReview(ctx, t.ID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	p, err := h.Data.GetProject(ctx, t.ProjectID)
	if err != nil {
		return err
	}
	for _, m := range v.Messages {
		if m.Role != "user" || m.ActorKind != "human" || (m.Action != "message" && m.Action != "discuss" && m.Action != "alignment_reply") || h.now().Sub(time.UnixMilli(m.At)) >= backfill {
			continue
		}
		for _, mention := range m.Mentions {
			if !slices.ContainsFunc(domain.ProjectMembers(p), func(member domain.ProjectMember) bool { return member.UserID == mention.UserID }) {
				continue
			}
			for _, name := range h.enabled() {
				b, e := h.Store.GetBinding(ctx, name, mention.UserID)
				if errors.Is(e, domain.ErrNotFound) {
					continue
				}
				if e != nil {
					return e
				}
				preview := []rune(strings.Join(strings.Fields(m.Body), " "))
				if len(preview) > 120 {
					preview = append(preview[:120], '…')
				}
				now := h.now().UnixMilli()
				d := Delivery{ID: mentionDeliveryID(m.ID, mention.UserID, name), UserID: mention.UserID, Channel: name, ExternalID: b.ExternalID, State: "pending", NextAt: now, UpdatedAt: now,
					Mention: &ReviewMentionNotice{TaskID: t.ID, ProjectID: t.ProjectID, MessageID: m.ID, Subject: t.Title, Project: p.Name, Sender: m.Actor, Preview: string(preview)}}
				if e = h.Store.QueueDelivery(ctx, d); e != nil {
					return e
				}
			}
		}
	}
	return nil
}

func (h *Hub) canDeliverMention(ctx context.Context, d Delivery) (bool, error) {
	p, err := h.Data.GetProject(ctx, d.Mention.ProjectID)
	if errors.Is(err, domain.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !slices.ContainsFunc(domain.ProjectMembers(p), func(m domain.ProjectMember) bool { return m.UserID == d.UserID }) {
		return false, nil
	}
	v, err := h.Data.GetRequirementReview(ctx, d.Mention.TaskID)
	if errors.Is(err, domain.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(v.Messages, func(m domain.RequirementReviewMessage) bool {
		return m.ID == d.Mention.MessageID && slices.ContainsFunc(m.Mentions, func(mention domain.ReviewMention) bool { return mention.UserID == d.UserID })
	}), nil
}

func (h *Hub) mentionCard(d Delivery) Card {
	m := d.Mention
	link := h.EntryURL(d.Channel, "/?"+url.Values{"task": {m.TaskID}, "review_message": {m.MessageID}}.Encode())
	return Card{ID: d.ID, Title: m.Sender + " 在需求评审中 @ 了你", Subject: m.Subject, Project: m.Project, Fields: []Field{{Label: "发言人", Value: m.Sender}, {Label: "消息", Value: m.Preview}}, URL: link, Buttons: []Button{{Text: "查看评审消息", URL: link, Primary: true}}}
}

func (h *Hub) MentionWarnings(ctx context.Context, m domain.RequirementReviewMessage) []string {
	out, seen := []string{}, map[string]bool{}
	if h == nil {
		return out
	}
	if _, ok := h.channel("wecom"); !ok {
		return out
	}
	for _, mention := range m.Mentions {
		if seen[mention.UserID] {
			continue
		}
		seen[mention.UserID] = true
		hint := ""
		if _, err := h.Store.GetBinding(ctx, "wecom", mention.UserID); err != nil {
			hint = "未绑定企业微信，无法推送"
		}
		if hint == "" {
			d, err := h.Store.GetDelivery(ctx, mentionDeliveryID(m.ID, mention.UserID, "wecom"))
			if err == nil && d.State == "failed" {
				hint = "企业微信推送失败，请联系管理员检查消息渠道"
			} else if err == nil && d.State == "dropped" {
				hint = "企业微信推送已取消"
			}
		}
		if hint != "" {
			out = append(out, mention.Name+"："+hint)
		}
	}
	return out
}

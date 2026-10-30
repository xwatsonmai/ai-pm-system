package agent

import (
	"ai-pm-system/internal/app"
	"ai-pm-system/internal/domain"
	"context"
	"errors"
)

// Repeat this policy in the current user turn as well as the system prompt:
// the same Pi session may contain an automatic result turn that forbids delegation.
const collaborationContinuationInstruction = `协查续查规则：先区分用户是查询进度，还是要求再次执行。用户说“继续刚才的问题”“重新查一次”“再试一下”，或说明环境/权限/连接配置已恢复并要求继续，属于续查请求，不只是催问；调查目标和环境已明确时不要再次要求用户确认访问条件。
续查先调用 get_collaborations 获取当前会话的最新任务。相同目标的 pending/investigating/blocked 任务仍未结束时复用，不另建重复任务；租约过期要如实说明等待重新认领，不能声称本地仍在执行。旧任务 completed 且用户要求继续原调查时，调用 create_collaboration 创建新一轮只读调查，沿用原目标和环境，brief 写明旧任务 ID、原取证时间、本轮用户补充和需要重新核验的内容；保留旧任务和旧结果，不把旧结果当成新证据。只查进度时返回最新状态，不新建。旧任务 canceled 后只有本轮用户明确要求重新排查才可新建，含糊的“继续”不能撤销用户先前的取消。
历史 cannot_access/inconclusive 仅说明当时未能访问或证据不足，不能据此断言现在仍不通，也不能凭“重试大概率失败”拒绝本轮续查。桌面客户端环境说明可能已在本地修改，平台没有自动获知或验证其变化；让新调查使用执行端最新配置，不要求用户把密钥发到平台。新工具成功返回任务 ID 后才说已重新发起，并说明待认领；工具失败则说明尚未发起成功。本规则适用于当前用户问答；历史自动结果消息中的“不要创建更多协同任务”仅限制那次自动续答，不能阻止后续用户明确要求续查。多任务指代不清时先澄清，不猜测或批量重建。`

type collaborationQuery struct {
	Collaborations []domain.CollaborationView `json:"collaborations"`
	AsOf           int64                      `json:"as_of"`
}

// Use the persisted user turn as authority, never the platform actor or model
// arguments. Recheck deletion and project membership on every tool call.
func (m *Manager) collaborationScope(ctx context.Context, r domain.Run) (domain.Actor, string, error) {
	if r.Kind != "question" || r.TriggerType == "collaboration_result" || r.OwnerID == "" || (r.OwnerKind != "human" && r.OwnerKind != "developer") {
		return domain.Actor{}, "", domain.ErrForbidden
	}
	current, err := m.App.Store.GetRun(ctx, r.ID)
	if err != nil {
		return domain.Actor{}, "", err
	}
	if current.Kind != r.Kind || current.TriggerType != r.TriggerType || current.ProjectID != r.ProjectID || current.ConversationID != r.ConversationID || current.OwnerID != r.OwnerID || current.OwnerKind != r.OwnerKind {
		return domain.Actor{}, "", domain.ErrForbidden
	}
	p, err := m.App.Store.GetProject(ctx, r.ProjectID)
	if err != nil {
		return domain.Actor{}, "", err
	}
	a, err := m.App.CollaborationOwner(ctx, r.OwnerID, r.OwnerKind, r.ProjectID)
	if err != nil {
		return a, "", err
	}
	if !app.CanReadRun(a, p, current) {
		return a, "", domain.ErrForbidden
	}
	conversation := r.ConversationID
	if conversation == "" {
		conversation = r.ID
	}
	return a, conversation, nil
}

func inCollaborationScope(v domain.Collaboration, a domain.Actor, conversation string) bool {
	return v.ProjectID == a.ProjectID && v.OwnerID == a.ID && v.OwnerKind == a.Kind && v.ConversationID == conversation
}

func (m *Manager) getConversationCollaborations(ctx context.Context, r domain.Run, id string) (collaborationQuery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := collaborationQuery{Collaborations: []domain.CollaborationView{}}
	a, conversation, err := m.collaborationScope(ctx, r)
	if err != nil {
		return out, err
	}
	var items []domain.Collaboration
	if id != "" {
		v, err := m.App.Store.GetCollaboration(ctx, id)
		if err != nil {
			return out, err
		}
		if !inCollaborationScope(v, a, conversation) {
			return out, domain.ErrForbidden
		}
		items = []domain.Collaboration{v}
	} else {
		items, err = m.App.Store.ListCollaborations(ctx, a.ProjectID)
		if err != nil {
			return out, err
		}
	}
	for _, v := range items {
		if !inCollaborationScope(v, a, conversation) {
			continue
		}
		view, err := m.App.GetCollaboration(ctx, a, v.ID)
		if err != nil {
			return out, err
		}
		out.Collaborations = append(out.Collaborations, view)
	}
	out.AsOf = domain.Now()
	return out, nil
}

func (m *Manager) cancelConversationCollaboration(ctx context.Context, r domain.Run, id string) (domain.CollaborationMutation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, conversation, err := m.collaborationScope(ctx, r)
	if err != nil {
		return domain.CollaborationMutation{}, err
	}
	if id == "" {
		return domain.CollaborationMutation{}, errors.New("请先查询并明确要取消的协同 ID")
	}
	v, err := m.App.Store.GetCollaboration(ctx, id)
	if err != nil {
		return domain.CollaborationMutation{}, err
	}
	if !inCollaborationScope(v, a, conversation) {
		return domain.CollaborationMutation{}, domain.ErrForbidden
	}
	if v.Status == "canceled" {
		return domain.CollaborationMutation{Collaboration: v}, nil
	}
	return m.cancelCollaboration(ctx, a, id, "cancel_collaboration:"+r.ID+":"+id)
}

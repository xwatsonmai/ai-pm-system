package notify

import (
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/storage"
	"context"
)

// Observed wraps the platform store so that every committed change to a task,
// its review or a project's members reaches the hub, whichever code path
// made it. A crash between the commit and the hub only delays the to-do
// until the next full pass.
type Observed struct {
	storage.Store
	Hub *Hub
}

func (o Observed) CommitTask(c context.Context, t domain.Task, expected int, ev domain.Event, r *domain.Receipt) error {
	err := o.Store.CommitTask(c, t, expected, ev, r)
	if err == nil {
		// Linked Bugs move their requirement in the same transaction.
		o.Hub.Changed(t.ID, t.ParentRequirementID)
	}
	return err
}

func (o Observed) CommitRequirementReview(c context.Context, v domain.RequirementReview, expected, revision int, t *domain.Task, ev *domain.Event, r *domain.Receipt, batches ...domain.MilestoneBatch) error {
	err := o.Store.CommitRequirementReview(c, v, expected, revision, t, ev, r, batches...)
	if err == nil {
		o.Hub.Changed(v.TaskID)
	}
	return err
}

func (o Observed) CommitVersionPlan(c context.Context, versions []domain.VersionWrite, tasks []domain.VersionTaskWrite, events []domain.Event, r *domain.Receipt, batches ...domain.MilestoneBatch) error {
	err := o.Store.CommitVersionPlan(c, versions, tasks, events, r, batches...)
	if err == nil {
		for _, t := range tasks {
			o.Hub.Changed(t.Value.ID, t.Value.ParentRequirementID)
		}
	}
	return err
}

func (o Observed) CommitRequirementIntake(c context.Context, v domain.RequirementIntake, expected int, t *domain.Task, ev *domain.Event, r *domain.Receipt, batches ...domain.MilestoneBatch) error {
	err := o.Store.CommitRequirementIntake(c, v, expected, t, ev, r, batches...)
	if err == nil && t != nil {
		o.Hub.Changed(t.ID)
	}
	return err
}

// PutProject may change who receives a project's to-dos.
func (o Observed) PutProject(c context.Context, p domain.Project) error {
	err := o.Store.PutProject(c, p)
	if err == nil {
		o.Hub.init()
		o.Hub.refreshAll()
	}
	return err
}

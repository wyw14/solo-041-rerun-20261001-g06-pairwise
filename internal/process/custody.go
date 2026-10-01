package process

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/wyw14/cry-97/internal/custody"
	"github.com/wyw14/cry-97/internal/model"
)

func (p *Plant) CollectSample(ctx context.Context, lineID model.LineID, sampleID uuid.UUID, point, sampler string) (custody.Task, error) {
	p.mu.RLock()
	_, ok := p.lines[lineID]
	p.mu.RUnlock()
	if !ok {
		return custody.Task{}, errors.New("process line is not found")
	}
	batch, ok := p.ActiveBatch(lineID)
	if !ok {
		return custody.Task{}, errors.New("process line has no active batch")
	}
	now := p.now()
	task, handover, err := p.custody.Collect(lineID, batch.ID, sampleID, point, sampler, now)
	if err != nil {
		return custody.Task{}, err
	}
	if err := p.journalHandover(ctx, task.LineID, handover, now); err != nil {
		return custody.Task{}, err
	}
	return task, nil
}

func (p *Plant) HandoverCustody(ctx context.Context, taskID uuid.UUID, action custody.Action, actor, note string) (custody.Task, error) {
	now := p.now()
	task, handover, err := p.custody.Transition(taskID, action, actor, note, now)
	if err != nil {
		return custody.Task{}, err
	}
	if err := p.journalHandover(ctx, task.LineID, handover, now); err != nil {
		return custody.Task{}, err
	}
	if task.Void() {
		p.voidCustodyChain(task, now)
	}
	return task, nil
}

func (p *Plant) journalHandover(ctx context.Context, lineID model.LineID, handover custody.Handover, now time.Time) error {
	p.mu.RLock()
	state, ok := p.lines[lineID]
	p.mu.RUnlock()
	if !ok {
		return errors.New("process line is not found")
	}
	event, err := model.NewEvent(lineID, state.Generation, model.EventCustodyHandover, handover, now)
	if err != nil {
		return err
	}
	if _, err := p.store.Append(ctx, event); err != nil {
		return err
	}
	return nil
}

// voidCustodyChain undoes what a lost or revoked sample previously qualified:
// the batch falls back to unqualified and any active discharge permit is revoked.
func (p *Plant) voidCustodyChain(task custody.Task, now time.Time) {
	p.mu.Lock()
	batch, ok := p.batches[task.BatchID]
	if ok && batch.QualificationSample == task.SampleID {
		if updated, err := batch.VoidQualification(task.SampleID, now); err == nil {
			p.batches[task.BatchID] = updated
			line := p.lines[updated.LineID]
			line.Stage = updated.Stage
			line.UpdatedAt = now.UTC()
			p.lines[updated.LineID] = line
		}
	}
	p.mu.Unlock()
	_, _ = p.permits.RevokeBatch(task.BatchID, now)
}

func (p *Plant) CustodyPending() []custody.Task {
	return p.custody.Pending()
}

func (p *Plant) CustodyArchived() []custody.Task {
	return p.custody.Archived()
}

func (p *Plant) CustodyTasks() []custody.Task {
	return p.custody.All()
}

func (p *Plant) CustodyTask(taskID uuid.UUID) (custody.Task, bool) {
	return p.custody.Get(taskID)
}

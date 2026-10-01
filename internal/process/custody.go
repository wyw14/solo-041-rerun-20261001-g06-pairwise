package process

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/wyw14/cry-97/internal/model"
)

// CustodyViews 是送检台视图：待接收（在途）与已归档（实验室已签收）。
type CustodyViews struct {
	Pending  []model.ChainOfCustody `json:"pending"`
	Archived []model.ChainOfCustody `json:"archived"`
}

// RegisterCustody 在现有传感器样本与处理批次之间建立人工采样送检链条。
// 采样任务必须落在已入库的传感器样本和该处理线当前有效批次上，登记即封样。
func (p *Plant) RegisterCustody(ctx context.Context, sampleID uuid.UUID, samplePoint, sampler string, sampledAt, sealedAt time.Time) (model.ChainOfCustody, error) {
	if err := ctx.Err(); err != nil {
		return model.ChainOfCustody{}, err
	}
	sample, found := p.catalog.Get(sampleID)
	if !found {
		return model.ChainOfCustody{}, errors.New("sensor sample is not found")
	}
	p.mu.RLock()
	batch, exists := p.batchesBySampleLine(sample.LineID)
	p.mu.RUnlock()
	if !exists || !batch.IsActive() {
		return model.ChainOfCustody{}, errors.New("sample line has no active process batch")
	}
	now := p.now().UTC()
	if sampledAt.IsZero() {
		sampledAt = now
	}
	if sealedAt.IsZero() {
		sealedAt = now
	}
	chain, err := model.NewChainOfCustody(sample, batch.ID, samplePoint, sampler, sampledAt, sealedAt)
	if err != nil {
		return model.ChainOfCustody{}, err
	}
	if _, err := p.appendCustodyEvent(ctx, chain); err != nil {
		return model.ChainOfCustody{}, err
	}
	if err := p.custodyBook.Register(chain); err != nil {
		return model.ChainOfCustody{}, err
	}
	return chain, nil
}

// ReceiveCustody 由实验室签收样本并归档，记录接收人和接收时间。
func (p *Plant) ReceiveCustody(ctx context.Context, sampleID uuid.UUID, receiver string, receivedAt time.Time) (model.ChainOfCustody, error) {
	if err := ctx.Err(); err != nil {
		return model.ChainOfCustody{}, err
	}
	chain, found := p.custodyBook.Get(sampleID)
	if !found {
		return model.ChainOfCustody{}, errors.New("custody chain is not found")
	}
	if receivedAt.IsZero() {
		receivedAt = p.now()
	}
	updated, err := chain.Receive(receiver, receivedAt)
	if err != nil {
		return model.ChainOfCustody{}, err
	}
	if _, err := p.appendCustodyEvent(ctx, updated); err != nil {
		return model.ChainOfCustody{}, err
	}
	if err := p.custodyBook.Replace(updated); err != nil {
		return model.ChainOfCustody{}, err
	}
	return updated, nil
}

// LoseCustody 标记在途样本丢失，丢失样本退出有效链条，后续不得开出出水许可。
func (p *Plant) LoseCustody(ctx context.Context, sampleID uuid.UUID, actor, note string, at time.Time) (model.ChainOfCustody, error) {
	return p.closeCustody(ctx, sampleID, actor, note, at,
		func(chain model.ChainOfCustody, stamp time.Time) (model.ChainOfCustody, error) {
			return chain.MarkLost(actor, stamp, note)
		})
}

// RevokeCustody 在实验室接收前撤销样本，撤销样本不得承载化验结果或许可。
func (p *Plant) RevokeCustody(ctx context.Context, sampleID uuid.UUID, actor, note string, at time.Time) (model.ChainOfCustody, error) {
	return p.closeCustody(ctx, sampleID, actor, note, at,
		func(chain model.ChainOfCustody, stamp time.Time) (model.ChainOfCustody, error) {
			return chain.Revoke(actor, stamp, note)
		})
}

func (p *Plant) closeCustody(
	ctx context.Context, sampleID uuid.UUID, actor, note string, at time.Time,
	mutate func(model.ChainOfCustody, time.Time) (model.ChainOfCustody, error),
) (model.ChainOfCustody, error) {
	if err := ctx.Err(); err != nil {
		return model.ChainOfCustody{}, err
	}
	chain, found := p.custodyBook.Get(sampleID)
	if !found {
		return model.ChainOfCustody{}, errors.New("custody chain is not found")
	}
	if at.IsZero() {
		at = p.now()
	}
	updated, err := mutate(chain, at)
	if err != nil {
		return model.ChainOfCustody{}, err
	}
	if _, err := p.appendCustodyEvent(ctx, updated); err != nil {
		return model.ChainOfCustody{}, err
	}
	if err := p.custodyBook.Replace(updated); err != nil {
		return model.ChainOfCustody{}, err
	}
	return updated, nil
}

func (p *Plant) Custody(sampleID uuid.UUID) (model.ChainOfCustody, bool) {
	return p.custodyBook.Get(sampleID)
}

func (p *Plant) CustodyViews() CustodyViews {
	return CustodyViews{Pending: p.custodyBook.Pending(), Archived: p.custodyBook.Archived()}
}

func (p *Plant) appendCustodyEvent(ctx context.Context, chain model.ChainOfCustody) (model.Event, error) {
	p.mu.RLock()
	state, ok := p.lines[chain.LineID]
	p.mu.RUnlock()
	if !ok {
		return model.Event{}, errors.New("custody line is not configured")
	}
	event, err := model.NewEvent(chain.LineID, state.Generation, model.EventCustodyChanged, chain, p.now())
	if err != nil {
		return model.Event{}, err
	}
	return p.store.Append(ctx, event)
}

// batchesBySampleLine 必须在 p.mu 持有下调用，返回处理线当前关联批次。
func (p *Plant) batchesBySampleLine(lineID model.LineID) (model.Batch, bool) {
	id, ok := p.lineBatches[lineID]
	if !ok {
		return model.Batch{}, false
	}
	batch, exists := p.batches[id]
	return batch, exists
}

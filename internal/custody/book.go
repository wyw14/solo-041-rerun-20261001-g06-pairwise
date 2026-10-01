package custody

import (
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/wyw14/cry-97/internal/model"
)

// Book 保存全部人工送检链条，并提供待接收与已归档视图。
// 每次登记、接收、丢失、撤销都以新的链条值整体替换存档，交接痕迹保留在链条内部。
type Book struct {
	mu     sync.RWMutex
	chains map[uuid.UUID]model.ChainOfCustody
}

func NewBook() *Book {
	return &Book{chains: make(map[uuid.UUID]model.ChainOfCustody)}
}

func (b *Book) Register(chain model.ChainOfCustody) error {
	if chain.SampleID == uuid.Nil {
		return errors.New("custody chain identity is incomplete")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := b.chains[chain.SampleID]; exists {
		return errors.New("custody chain already registered for sample")
	}
	b.chains[chain.SampleID] = chain
	return nil
}

func (b *Book) Receive(sampleID uuid.UUID, receiver string, at time.Time) (model.ChainOfCustody, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	chain, ok := b.chains[sampleID]
	if !ok {
		return model.ChainOfCustody{}, errors.New("custody chain is not found")
	}
	updated, err := chain.Receive(receiver, at)
	if err != nil {
		return model.ChainOfCustody{}, err
	}
	b.chains[sampleID] = updated
	return updated, nil
}

func (b *Book) MarkLost(sampleID uuid.UUID, actor string, at time.Time, note string) (model.ChainOfCustody, error) {
	return b.close(sampleID, func(chain model.ChainOfCustody) (model.ChainOfCustody, error) {
		return chain.MarkLost(actor, at, note)
	})
}

func (b *Book) Revoke(sampleID uuid.UUID, actor string, at time.Time, note string) (model.ChainOfCustody, error) {
	return b.close(sampleID, func(chain model.ChainOfCustody) (model.ChainOfCustody, error) {
		return chain.Revoke(actor, at, note)
	})
}

func (b *Book) close(sampleID uuid.UUID, mutate func(model.ChainOfCustody) (model.ChainOfCustody, error)) (model.ChainOfCustody, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	chain, ok := b.chains[sampleID]
	if !ok {
		return model.ChainOfCustody{}, errors.New("custody chain is not found")
	}
	updated, err := mutate(chain)
	if err != nil {
		return model.ChainOfCustody{}, err
	}
	b.chains[sampleID] = updated
	return updated, nil
}

// Replace 用给定链条覆盖存档，仅用于日志回放恢复。
func (b *Book) Replace(chain model.ChainOfCustody) error {
	if chain.SampleID == uuid.Nil {
		return errors.New("custody chain identity is incomplete")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.chains[chain.SampleID] = chain
	return nil
}

func (b *Book) Get(sampleID uuid.UUID) (model.ChainOfCustody, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	chain, ok := b.chains[sampleID]
	return chain, ok
}

// Pending 待接收视图：已封样但实验室尚未接收（含在途），按采样时间倒序。
func (b *Book) Pending() []model.ChainOfCustody {
	return b.filter(func(chain model.ChainOfCustody) bool {
		return chain.Status == model.ChainPending
	}, func(a, c model.ChainOfCustody) bool { return a.SampledAt.After(c.SampledAt) })
}

// Archived 已归档视图：实验室完成接收，按接收时间倒序。
func (b *Book) Archived() []model.ChainOfCustody {
	return b.filter(func(chain model.ChainOfCustody) bool {
		return chain.Status == model.ChainArchived
	}, func(a, c model.ChainOfCustody) bool {
		return a.ReceivedAt != nil && c.ReceivedAt != nil && a.ReceivedAt.After(*c.ReceivedAt)
	})
}

func (b *Book) All() []model.ChainOfCustody {
	return b.filter(func(model.ChainOfCustody) bool { return true },
		func(a, c model.ChainOfCustody) bool { return a.SampledAt.After(c.SampledAt) })
}

func (b *Book) filter(keep func(model.ChainOfCustody) bool, less func(a, c model.ChainOfCustody) bool) []model.ChainOfCustody {
	b.mu.RLock()
	defer b.mu.RUnlock()
	result := make([]model.ChainOfCustody, 0, len(b.chains))
	for _, chain := range b.chains {
		if keep(chain) {
			result = append(result, chain)
		}
	}
	sort.Slice(result, func(i, j int) bool { return less(result[i], result[j]) })
	return result
}

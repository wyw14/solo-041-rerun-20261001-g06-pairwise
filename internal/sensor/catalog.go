package sensor

import (
	"sync"

	"github.com/google/uuid"
	"github.com/wyw14/cry-97/internal/model"
)

// Catalog 按样本 ID 留存已经入库的传感器样本，供人工送检链条登记时核验，
// 保证链条只能建立在现有传感器样本之上。
type Catalog struct {
	mu      sync.RWMutex
	byID    map[uuid.UUID]model.Sample
}

func NewCatalog() *Catalog {
	return &Catalog{byID: make(map[uuid.UUID]model.Sample)}
}

func (c *Catalog) Add(sample model.Sample) {
	if sample.ID == uuid.Nil {
		return
	}
	c.mu.Lock()
	c.byID[sample.ID] = sample
	c.mu.Unlock()
}

func (c *Catalog) Get(id uuid.UUID) (model.Sample, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	sample, ok := c.byID[id]
	return sample, ok
}

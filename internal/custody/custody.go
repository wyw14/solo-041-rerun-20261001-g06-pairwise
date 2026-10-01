package custody

import (
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/wyw14/cry-97/internal/model"
)

type TaskState string

const (
	TaskCollected TaskState = "collected"
	TaskSealed    TaskState = "sealed"
	TaskReceived  TaskState = "received"
	TaskArchived  TaskState = "archived"
	TaskLost      TaskState = "lost"
	TaskRevoked   TaskState = "revoked"
)

type Action string

const (
	ActionSeal    Action = "seal"
	ActionReceive Action = "receive"
	ActionArchive Action = "archive"
	ActionLost    Action = "lost"
	ActionRevoke  Action = "revoke"
)

func ParseAction(raw string) (Action, error) {
	action := Action(strings.TrimSpace(raw))
	switch action {
	case ActionSeal, ActionReceive, ActionArchive, ActionLost, ActionRevoke:
		return action, nil
	default:
		return "", errors.New("custody action is unsupported")
	}
}

// transitions maps each action to the states it may start from and the state it enters.
var transitions = map[Action]struct {
	from []TaskState
	to   TaskState
}{
	ActionSeal:    {from: []TaskState{TaskCollected}, to: TaskSealed},
	ActionReceive: {from: []TaskState{TaskSealed}, to: TaskReceived},
	ActionArchive: {from: []TaskState{TaskReceived}, to: TaskArchived},
	ActionLost:    {from: []TaskState{TaskCollected, TaskSealed, TaskReceived}, to: TaskLost},
	ActionRevoke:  {from: []TaskState{TaskCollected, TaskSealed, TaskReceived}, to: TaskRevoked},
}

type Handover struct {
	ID         uuid.UUID `json:"id"`
	TaskID     uuid.UUID `json:"task_id"`
	From       TaskState `json:"from"`
	To         TaskState `json:"to"`
	Actor      string    `json:"actor"`
	Note       string    `json:"note,omitempty"`
	OccurredAt time.Time `json:"occurred_at"`
}

type Task struct {
	ID         uuid.UUID    `json:"id"`
	LineID     model.LineID `json:"line_id"`
	BatchID    uuid.UUID    `json:"batch_id"`
	SampleID   uuid.UUID    `json:"sample_id"`
	Point      string       `json:"point"`
	Sampler    string       `json:"sampler"`
	State      TaskState    `json:"state"`
	CreatedAt  time.Time    `json:"created_at"`
	SealedAt   *time.Time   `json:"sealed_at,omitempty"`
	ReceivedAt *time.Time   `json:"received_at,omitempty"`
	ArchivedAt *time.Time   `json:"archived_at,omitempty"`
	ClosedAt   *time.Time   `json:"closed_at,omitempty"`
	Trail      []Handover   `json:"trail"`
}

func (t Task) ChainValid() bool {
	return t.State == TaskReceived || t.State == TaskArchived
}

func (t Task) Void() bool {
	return t.State == TaskLost || t.State == TaskRevoked
}

type Book struct {
	mu       sync.RWMutex
	tasks    map[uuid.UUID]Task
	bySample map[uuid.UUID]uuid.UUID
}

func NewBook() *Book {
	return &Book{tasks: make(map[uuid.UUID]Task), bySample: make(map[uuid.UUID]uuid.UUID)}
}

func (b *Book) Collect(lineID model.LineID, batchID, sampleID uuid.UUID, point, sampler string, now time.Time) (Task, Handover, error) {
	point = strings.TrimSpace(point)
	sampler = strings.TrimSpace(sampler)
	if lineID == "" || batchID == uuid.Nil || sampleID == uuid.Nil || point == "" || sampler == "" {
		return Task{}, Handover{}, errors.New("custody task identity is incomplete")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, taken := b.bySample[sampleID]; taken {
		return Task{}, Handover{}, errors.New("custody sample already has a chain")
	}
	stamp := now.UTC()
	task := Task{
		ID: uuid.New(), LineID: lineID, BatchID: batchID, SampleID: sampleID,
		Point: point, Sampler: sampler, State: TaskCollected, CreatedAt: stamp,
	}
	handover := Handover{
		ID: uuid.New(), TaskID: task.ID, To: TaskCollected, Actor: sampler,
		Note: "sample collected at " + point, OccurredAt: stamp,
	}
	task.Trail = []Handover{handover}
	b.tasks[task.ID] = task
	b.bySample[sampleID] = task.ID
	return task, handover, nil
}

func (b *Book) Transition(taskID uuid.UUID, action Action, actor, note string, now time.Time) (Task, Handover, error) {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return Task{}, Handover{}, errors.New("custody handover actor is required")
	}
	rule, ok := transitions[action]
	if !ok {
		return Task{}, Handover{}, errors.New("custody action is unsupported")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	task, ok := b.tasks[taskID]
	if !ok {
		return Task{}, Handover{}, errors.New("custody task is not found")
	}
	allowed := false
	for _, state := range rule.from {
		if task.State == state {
			allowed = true
			break
		}
	}
	if !allowed {
		return Task{}, Handover{}, errors.New("custody task cannot " + string(action) + " from " + string(task.State))
	}
	stamp := now.UTC()
	handover := Handover{
		ID: uuid.New(), TaskID: task.ID, From: task.State, To: rule.to,
		Actor: actor, Note: strings.TrimSpace(note), OccurredAt: stamp,
	}
	task.State = rule.to
	switch rule.to {
	case TaskSealed:
		task.SealedAt = &stamp
	case TaskReceived:
		task.ReceivedAt = &stamp
	case TaskArchived:
		task.ArchivedAt = &stamp
	case TaskLost, TaskRevoked:
		task.ClosedAt = &stamp
	}
	task.Trail = append(append([]Handover{}, task.Trail...), handover)
	b.tasks[taskID] = task
	return task, handover, nil
}

// EnsureChainValid reports whether a lab result may match the sample: the
// chain must exist, have reached the lab, and not be lost or revoked.
func (b *Book) EnsureChainValid(sampleID uuid.UUID) error {
	b.mu.RLock()
	defer b.mu.RUnlock()
	taskID, ok := b.bySample[sampleID]
	if !ok {
		return errors.New("custody chain is not found for sample")
	}
	task := b.tasks[taskID]
	switch {
	case task.State == TaskLost:
		return errors.New("custody sample is lost")
	case task.State == TaskRevoked:
		return errors.New("custody sample is revoked")
	case !task.ChainValid():
		return errors.New("custody sample has not reached the lab")
	}
	return nil
}

func (b *Book) Get(taskID uuid.UUID) (Task, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	task, ok := b.tasks[taskID]
	return task, ok
}

func (b *Book) BySample(sampleID uuid.UUID) (Task, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	taskID, ok := b.bySample[sampleID]
	if !ok {
		return Task{}, false
	}
	task, ok := b.tasks[taskID]
	return task, ok
}

// Pending lists chains still waiting for laboratory receipt.
func (b *Book) Pending() []Task {
	b.mu.RLock()
	defer b.mu.RUnlock()
	result := make([]Task, 0)
	for _, task := range b.tasks {
		if task.State == TaskCollected || task.State == TaskSealed {
			result = append(result, task)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result
}

// Archived lists chains whose results are filed.
func (b *Book) Archived() []Task {
	b.mu.RLock()
	defer b.mu.RUnlock()
	result := make([]Task, 0)
	for _, task := range b.tasks {
		if task.State == TaskArchived {
			result = append(result, task)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ArchivedAt == nil || result[j].ArchivedAt == nil {
			return result[i].CreatedAt.After(result[j].CreatedAt)
		}
		return result[i].ArchivedAt.After(*result[j].ArchivedAt)
	})
	return result
}

func (b *Book) All() []Task {
	b.mu.RLock()
	defer b.mu.RUnlock()
	result := make([]Task, 0, len(b.tasks))
	for _, task := range b.tasks {
		result = append(result, task)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	return result
}

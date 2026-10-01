package model

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

type ChainStatus string

const (
	// ChainPending 已采样封样、等待实验室接收。
	ChainPending ChainStatus = "pending"
	// ChainArchived 实验室已接收归档，是唯一可承载化验结果的有效链条。
	ChainArchived ChainStatus = "archived"
	// ChainLost 样本在送检途中丢失。
	ChainLost ChainStatus = "lost"
	// ChainRevoked 样本在实验室接收前被撤销。
	ChainRevoked ChainStatus = "revoked"
)

type HandoverAction string

const (
	HandoverRegistered HandoverAction = "registered"
	HandoverSealed     HandoverAction = "sealed"
	HandoverReceived   HandoverAction = "received"
	HandoverLost       HandoverAction = "lost"
	HandoverRevoked    HandoverAction = "revoked"
)

// Handover 记录链条上的一次交接：谁、在何时、做了什么。
type Handover struct {
	Seq        int            `json:"seq"`
	Action     HandoverAction `json:"action"`
	Actor      string         `json:"actor"`
	OccurredAt time.Time      `json:"occurred_at"`
	Note       string         `json:"note,omitempty"`
}

// ChainOfCustody 是一个人工送检样本从采样点到实验室的完整链条。
// 链条与传感器样本（model.Sample）一一对应：化验结果只能挂在已归档链条的样本上。
type ChainOfCustody struct {
	SampleID    uuid.UUID  `json:"sample_id"`
	LineID      LineID     `json:"line_id"`
	BatchID     uuid.UUID  `json:"batch_id"`
	SamplePoint string     `json:"sample_point"`
	Sampler     string     `json:"sampler"`
	Kind        SampleKind `json:"kind"`
	Status      ChainStatus `json:"status"`
	SampledAt   time.Time  `json:"sampled_at"`
	SealedAt    *time.Time `json:"sealed_at,omitempty"`
	ReceivedAt  *time.Time `json:"received_at,omitempty"`
	ClosedAt    *time.Time `json:"closed_at,omitempty"`
	Handovers   []Handover `json:"handovers"`
}

// NewChainOfCustody 登记一次人工采样任务：记录采样点、处理批次、采样者和采样时间，
// 采样完成即封样，封样后等待实验室接收。
func NewChainOfCustody(sample Sample, batchID uuid.UUID, samplePoint, sampler string, sampledAt, sealedAt time.Time) (ChainOfCustody, error) {
	if sample.ID == uuid.Nil || sample.LineID == "" || sample.Kind == "" {
		return ChainOfCustody{}, errors.New("custody sample identity is incomplete")
	}
	if batchID == uuid.Nil {
		return ChainOfCustody{}, errors.New("custody batch is required")
	}
	point := strings.TrimSpace(samplePoint)
	name := strings.TrimSpace(sampler)
	if point == "" {
		return ChainOfCustody{}, errors.New("custody sample point is required")
	}
	if name == "" {
		return ChainOfCustody{}, errors.New("custody sampler is required")
	}
	sampled := sampledAt.UTC()
	sealed := sealedAt.UTC()
	if sampled.Equal(time.Time{}) {
		return ChainOfCustody{}, errors.New("custody sampled time is required")
	}
	if sealed.Equal(time.Time{}) {
		return ChainOfCustody{}, errors.New("custody sealed time is required")
	}
	if sealed.Before(sampled) {
		return ChainOfCustody{}, errors.New("custody sealed time is before sampled time")
	}
	chain := ChainOfCustody{
		SampleID: sample.ID, LineID: sample.LineID, BatchID: batchID,
		SamplePoint: point, Sampler: name, Kind: sample.Kind,
		Status: ChainPending, SampledAt: sampled, SealedAt: &sealed,
		Handovers: make([]Handover, 0, 3),
	}
	chain.Handovers = append(chain.Handovers,
		Handover{Seq: 1, Action: HandoverRegistered, Actor: name, OccurredAt: sampled},
		Handover{Seq: 2, Action: HandoverSealed, Actor: name, OccurredAt: sealed},
	)
	return chain, nil
}

// Receive 由实验室接收并归档样本，返回新的链条副本。
func (c ChainOfCustody) Receive(receiver string, receivedAt time.Time) (ChainOfCustody, error) {
	name := strings.TrimSpace(receiver)
	if name == "" {
		return c, errors.New("custody receiver is required")
	}
	if c.Status != ChainPending {
		return c, errors.New("only pending custody samples can be received")
	}
	stamp := receivedAt.UTC()
	if stamp.Equal(time.Time{}) {
		return c, errors.New("custody received time is required")
	}
	if c.SealedAt != nil && stamp.Before(*c.SealedAt) {
		return c, errors.New("custody received time is before sealed time")
	}
	c.Status = ChainArchived
	c.ReceivedAt = &stamp
	c.ClosedAt = &stamp
	c.Handovers = append(c.Handovers, Handover{
		Seq: len(c.Handovers) + 1, Action: HandoverReceived, Actor: name, OccurredAt: stamp,
	})
	return c, nil
}

// MarkLost 将样本标记为送检丢失，丢失样本永久退出有效链条。
func (c ChainOfCustody) MarkLost(actor string, at time.Time, note string) (ChainOfCustody, error) {
	name := strings.TrimSpace(actor)
	if name == "" {
		return c, errors.New("custody actor is required")
	}
	if c.Status != ChainPending {
		return c, errors.New("only pending custody samples can be marked lost")
	}
	return c.close(ChainLost, HandoverLost, name, at, note)
}

// Revoke 在实验室接收前撤销样本，撤销后的样本不得再用于化验和许可。
func (c ChainOfCustody) Revoke(actor string, at time.Time, note string) (ChainOfCustody, error) {
	name := strings.TrimSpace(actor)
	if name == "" {
		return c, errors.New("custody actor is required")
	}
	if c.Status != ChainPending {
		return c, errors.New("only pending custody samples can be revoked")
	}
	return c.close(ChainRevoked, HandoverRevoked, name, at, note)
}

func (c ChainOfCustody) close(status ChainStatus, action HandoverAction, actor string, at time.Time, note string) (ChainOfCustody, error) {
	stamp := at.UTC()
	if stamp.Equal(time.Time{}) {
		return c, errors.New("custody close time is required")
	}
	if last := c.Handovers[len(c.Handovers)-1]; stamp.Before(last.OccurredAt) {
		return c, errors.New("custody close time is before the previous handover")
	}
	c.Status = status
	c.ClosedAt = &stamp
	c.Handovers = append(c.Handovers, Handover{
		Seq: len(c.Handovers) + 1, Action: action, Actor: actor, OccurredAt: stamp, Note: strings.TrimSpace(note),
	})
	return c, nil
}

// Archived 表示链条已被实验室接收归档。
func (c ChainOfCustody) Archived() bool { return c.Status == ChainArchived }

// AcceptsResults 只有有效（已归档）链条中的样本可以承载化验结果，
// 丢失或撤销样本一律拒绝。
func (c ChainOfCustody) AcceptsResults() bool { return c.Archived() }

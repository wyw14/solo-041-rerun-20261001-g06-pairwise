package process

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wyw14/cry-97/internal/model"
)

func newTestPlant(t *testing.T) (*Plant, time.Time) {
	t.Helper()
	clock := time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC)
	ticks := 0
	plant, err := NewPlant(t.TempDir(), []model.LineID{"line-1"}, func() time.Time {
		ticks++
		return clock.Add(time.Duration(ticks) * time.Minute)
	})
	if err != nil {
		t.Fatalf("new plant: %v", err)
	}
	return plant, clock
}

func activeSample(t *testing.T, plant *Plant, line model.LineID, seq uint64) model.Sample {
	t.Helper()
	sample := model.NewSample(line, "basin-1", model.SampleCOD, seq, 32.5, "mg/L", plant.Clock())
	if err := plant.ReceiveSample(context.Background(), sample); err != nil {
		t.Fatalf("receive sample: %v", err)
	}
	return sample
}

func qualifiedResult(sample model.Sample, batchID uuid.UUID, at time.Time) model.LabResult {
	return model.LabResult{
		SampleID: sample.ID, LineID: sample.LineID, BatchID: batchID,
		Revision: 1, COD: 30, Ammonia: 2, Valid: true, ReceivedAt: at,
	}
}

// 完整链条：登记封样 → 实验室接收归档 → 结果合格 → 开出出水许可。
func TestCustodyChainQualifiesBatchAndOpensPermit(t *testing.T) {
	ctx := context.Background()
	plant, base := newTestPlant(t)
	batch, err := plant.StartBatch(ctx, "line-1", 760, "north-intake")
	if err != nil {
		t.Fatalf("start batch: %v", err)
	}
	sample := activeSample(t, plant, "line-1", 1)

	sealed := base.Add(10 * time.Minute)
	chain, err := plant.RegisterCustody(ctx, sample.ID, "二沉池出水堰 SD1", "王采样", base.Add(5*time.Minute), sealed)
	if err != nil {
		t.Fatalf("register custody: %v", err)
	}
	if chain.Status != model.ChainPending || chain.ReceivedAt != nil {
		t.Fatalf("new chain should be pending, got %s", chain.Status)
	}
	if len(chain.Handovers) != 2 || chain.Handovers[1].Action != model.HandoverSealed {
		t.Fatalf("new chain should record register+seal handovers, got %+v", chain.Handovers)
	}

	// 未归档样本：结果必须被拒绝，且不得开许可。
	outcome, err := plant.ApplyLabResult(ctx, qualifiedResult(sample, batch.ID, sealed.Add(time.Minute)), plant.Clock())
	if err == nil {
		t.Fatalf("lab result for pending sample must be rejected, outcome=%+v", outcome)
	}
	if permits := plant.Permits(); len(permits) != 0 {
		t.Fatalf("no permit may exist before archive, got %+v", permits)
	}

	received := sealed.Add(30 * time.Minute)
	chain, err = plant.ReceiveCustody(ctx, sample.ID, "李化验", received)
	if err != nil {
		t.Fatalf("receive custody: %v", err)
	}
	if !chain.Archived() || chain.ReceivedAt == nil {
		t.Fatalf("chain should be archived at %s", received)
	}
	if last := chain.Handovers[len(chain.Handovers)-1]; last.Action != model.HandoverReceived || last.Actor != "李化验" {
		t.Fatalf("receive handover not recorded: %+v", last)
	}

	outcome, err = plant.ApplyLabResult(ctx, qualifiedResult(sample, batch.ID, received.Add(time.Minute)), plant.Clock())
	if err != nil {
		t.Fatalf("qualified result on archived chain should apply: %v", err)
	}
	if outcome.Permit == nil || !outcome.Permit.Active {
		t.Fatalf("qualified archived sample must open an active permit, got %+v", outcome.Permit)
	}

	views := plant.CustodyViews()
	if len(views.Archived) != 1 || len(views.Pending) != 0 {
		t.Fatalf("views after receive wrong: pending=%d archived=%d", len(views.Pending), len(views.Archived))
	}
}

// 丢失样本：结果被拒、不得开许可。
func TestLostSampleCannotProducePermit(t *testing.T) {
	ctx := context.Background()
	plant, base := newTestPlant(t)
	batch, err := plant.StartBatch(ctx, "line-1", 760, "north-intake")
	if err != nil {
		t.Fatalf("start batch: %v", err)
	}
	sample := activeSample(t, plant, "line-1", 1)
	chain, err := plant.RegisterCustody(ctx, sample.ID, "SD1", "王采样", base.Add(5*time.Minute), base.Add(10*time.Minute))
	if err != nil {
		t.Fatalf("register custody: %v", err)
	}
	chain, err = plant.LoseCustody(ctx, sample.ID, "张运输", "冷链箱遗落", base.Add(40*time.Minute))
	if err != nil {
		t.Fatalf("mark lost: %v", err)
	}
	if chain.Status != model.ChainLost {
		t.Fatalf("status = %s, want lost", chain.Status)
	}
	if _, err := plant.ApplyLabResult(ctx, qualifiedResult(sample, batch.ID, base.Add(2*time.Hour)), plant.Clock()); err == nil {
		t.Fatal("lab result for lost sample must be rejected")
	}
	if permits := plant.Permits(); len(permits) != 0 {
		t.Fatalf("lost sample must never open a permit: %+v", permits)
	}
	// 丢失后不允许再接收。
	if _, err := plant.ReceiveCustody(ctx, sample.ID, "李化验", base.Add(3*time.Hour)); err == nil {
		t.Fatal("lost chain must not be receivable")
	}
}

// 撤销样本：结果被拒、不得开许可；待接收/已归档视图都不再出现。
func TestRevokedSampleExcludedFromViews(t *testing.T) {
	ctx := context.Background()
	plant, base := newTestPlant(t)
	if _, err := plant.StartBatch(ctx, "line-1", 760, "north-intake"); err != nil {
		t.Fatalf("start batch: %v", err)
	}
	sample := activeSample(t, plant, "line-1", 1)
	if _, err := plant.RegisterCustody(ctx, sample.ID, "SD1", "王采样", base.Add(5*time.Minute), base.Add(10*time.Minute)); err != nil {
		t.Fatalf("register custody: %v", err)
	}
	chain, err := plant.RevokeCustody(ctx, sample.ID, "王采样", "采样污染作废", base.Add(15*time.Minute))
	if err != nil {
		t.Fatalf("revoke custody: %v", err)
	}
	if chain.Status != model.ChainRevoked || len(chain.Handovers) != 3 {
		t.Fatalf("revoked chain should keep 3 handovers, got %+v", chain)
	}
	views := plant.CustodyViews()
	if len(views.Pending) != 0 || len(views.Archived) != 0 {
		t.Fatalf("revoked sample must leave both views, got pending=%d archived=%d", len(views.Pending), len(views.Archived))
	}
}

// 链条只能建立在现有传感器样本之上，且批次/处理线必须匹配。
func TestCustodyRequiresExistingSampleAndMatchingBatch(t *testing.T) {
	ctx := context.Background()
	plant, base := newTestPlant(t)
	if _, err := plant.StartBatch(ctx, "line-1", 760, "north-intake"); err != nil {
		t.Fatalf("start batch: %v", err)
	}
	if _, err := plant.RegisterCustody(ctx, uuid.New(), "SD1", "王采样", base, base); err == nil {
		t.Fatal("custody for unknown sensor sample must be rejected")
	}
	sample := activeSample(t, plant, "line-1", 1)
	if _, err := plant.RegisterCustody(ctx, sample.ID, "SD1", "王采样", base.Add(10*time.Minute), base.Add(5*time.Minute)); err == nil {
		t.Fatal("seal-before-sample time must be rejected")
	}
}

// 结果的批次/处理线与链条不一致时拒绝。
func TestLabResultMustMatchChainIdentity(t *testing.T) {
	ctx := context.Background()
	plant, base := newTestPlant(t)
	batch, err := plant.StartBatch(ctx, "line-1", 760, "north-intake")
	if err != nil {
		t.Fatalf("start batch: %v", err)
	}
	sample := activeSample(t, plant, "line-1", 1)
	if _, err := plant.RegisterCustody(ctx, sample.ID, "SD1", "王采样", base.Add(5*time.Minute), base.Add(10*time.Minute)); err != nil {
		t.Fatalf("register custody: %v", err)
	}
	if _, err := plant.ReceiveCustody(ctx, sample.ID, "李化验", base.Add(40*time.Minute)); err != nil {
		t.Fatalf("receive custody: %v", err)
	}
	foreign := qualifiedResult(sample, batch.ID, base.Add(2*time.Hour))
	foreign.BatchID = uuid.New()
	if _, err := plant.ApplyLabResult(ctx, foreign, plant.Clock()); err == nil {
		t.Fatal("result batch mismatch must be rejected")
	}
	early := qualifiedResult(sample, batch.ID, base.Add(20*time.Minute)) // 早于接收时间
	if _, err := plant.ApplyLabResult(ctx, early, plant.Clock()); err == nil {
		t.Fatal("result predating custody receipt must be rejected")
	}
}

// 重启回放后链条台账与样本目录恢复，视图与交接痕迹完整。
func TestCustodyRecoversFromJournal(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	base := time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC)
	ticks := 0
	clock := func() time.Time { ticks++; return base.Add(time.Duration(ticks) * time.Minute) }

	first, err := NewPlant(dataDir, []model.LineID{"line-1"}, clock)
	if err != nil {
		t.Fatalf("first plant: %v", err)
	}
	batch, err := first.StartBatch(ctx, "line-1", 760, "north-intake")
	if err != nil {
		t.Fatalf("start batch: %v", err)
	}
	sample := activeSample(t, first, "line-1", 1)
	if _, err := first.RegisterCustody(ctx, sample.ID, "SD1", "王采样", base.Add(5*time.Minute), base.Add(10*time.Minute)); err != nil {
		t.Fatalf("register custody: %v", err)
	}
	if _, err := first.ReceiveCustody(ctx, sample.ID, "李化验", base.Add(40*time.Minute)); err != nil {
		t.Fatalf("receive custody: %v", err)
	}
	if _, err := first.ApplyLabResult(ctx, qualifiedResult(sample, batch.ID, base.Add(2*time.Hour)), first.Clock()); err != nil {
		t.Fatalf("apply result: %v", err)
	}

	second, err := NewPlant(dataDir, []model.LineID{"line-1"}, clock)
	if err != nil {
		t.Fatalf("second plant: %v", err)
	}
	if _, err := second.RecoverLine(ctx, "line-1"); err != nil {
		t.Fatalf("recover: %v", err)
	}
	chain, ok := second.Custody(sample.ID)
	if !ok || !chain.Archived() {
		t.Fatalf("chain not recovered: ok=%v status=%s", ok, chain.Status)
	}
	if len(chain.Handovers) != 3 {
		t.Fatalf("handovers not recovered: %+v", chain.Handovers)
	}
	if _, ok := second.catalog.Get(sample.ID); !ok {
		t.Fatal("sensor catalog not recovered")
	}
}

package process

import (
	"context"
	"errors"

	"github.com/wyw14/cry-97/internal/journal"
	"github.com/wyw14/cry-97/internal/model"
)

func (p *Plant) RecoverLine(ctx context.Context, lineID model.LineID) (uint64, error) {
	if snapshot, found, err := p.snapshots.Load(lineID); err != nil {
		return 0, err
	} else if found {
		p.mu.Lock()
		p.lines[lineID] = snapshot
		p.mu.Unlock()
	}
	replayer, err := journal.NewReplayer(p.store, p)
	if err != nil {
		return 0, err
	}
	return replayer.ReplayLine(ctx, lineID)
}

func (p *Plant) ApplyRecovered(ctx context.Context, event model.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	state, ok := p.lines[event.LineID]
	if !ok {
		return errors.New("recovery line is not configured")
	}
	if event.Sequence <= state.LastEventSeq {
		return nil
	}
	if state.Emergency && event.Generation < state.Generation {
		state.LastEventSeq = event.Sequence
		p.lines[event.LineID] = state
		return nil
	}
	if event.IsTerminal() {
		var record EmergencyRecord
		if err := event.Decode(&record); err != nil {
			return err
		}
		if record.LineID != event.LineID || record.Generation != event.Generation {
			return errors.New("emergency recovery payload does not match event identity")
		}
		state = state.Isolate(event.Sequence, event.OccurredAt)
		p.lines[event.LineID] = state
		p.devices.StopLine(event.LineID, state.Generation)
		return nil
	}
	if event.Kind == model.EventDeviceCommanded {
		var command model.DeviceCommand
		if err := event.Decode(&command); err != nil {
			return err
		}
		if err := p.devices.Publish(ctx, command); err != nil {
			return err
		}
	}
	if event.Kind == model.EventSampleStored {
		var sample model.Sample
		if err := event.Decode(&sample); err != nil {
			return err
		}
		p.catalog.Add(sample)
	}
	if event.Kind == model.EventCustodyChanged {
		var chain model.ChainOfCustody
		if err := event.Decode(&chain); err != nil {
			return err
		}
		if chain.LineID != event.LineID {
			return errors.New("custody recovery payload does not match event line")
		}
		if err := p.custodyBook.Replace(chain); err != nil {
			return err
		}
	}
	state.LastEventSeq = event.Sequence
	state.UpdatedAt = event.OccurredAt
	p.lines[event.LineID] = state
	return nil
}

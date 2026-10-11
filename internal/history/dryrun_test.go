package history

import (
	"context"
	"testing"
)

type countingStore struct{ reads, writes int }

func (s *countingStore) Previous(context.Context, string, string) (*Snapshot, error) {
	s.reads++
	return &Snapshot{}, nil
}
func (s *countingStore) Save(context.Context, Snapshot) error { s.writes++; return nil }

func TestDryRunReadsButDoesNotWrite(t *testing.T) {
	base := &countingStore{}
	dry := DryRun{Store: base}
	if got, err := dry.Previous(context.Background(), "AMD", "2026-10-12"); err != nil || got == nil {
		t.Fatalf("Previous = %#v, %v", got, err)
	}
	if got, err := dry.History(context.Background(), "AMD", "2026-10-12"); err != nil || len(got) != 1 {
		t.Fatalf("History = %#v, %v", got, err)
	}
	if err := dry.Save(context.Background(), Snapshot{}); err != nil {
		t.Fatal(err)
	}
	if base.reads != 2 || base.writes != 0 {
		t.Fatalf("dry run must read through and never write: %+v", base)
	}
}

package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAllowsNoneTrigger(t *testing.T) {
	p := filepath.Join(t.TempDir(), "watchlist.yaml")
	y := "watchlist:\n  - ticker: AMD\n    held: false\n    trigger: {type: none, value: null}\n    rationale: watch only\n"
	if err := os.WriteFile(p, []byte(y), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.Watchlist[0].Trigger.Type; got != "none" {
		t.Fatalf("got %q", got)
	}
}

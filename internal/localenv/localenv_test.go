package localenv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(p, []byte("# test\nONE=1\nTWO=\"two words\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Load(p); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("ONE") != "1" || os.Getenv("TWO") != "two words" {
		t.Fatal("dotenv values not loaded")
	}
}

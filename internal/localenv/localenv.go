// Package localenv provides an intentionally small, opt-in dotenv reader for
// local development. It is never invoked by the Cloud Run deployment path.
package localenv

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

func Load(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open local env file: %w", err)
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for line := 1; s.Scan(); line++ {
		text := strings.TrimSpace(s.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		text = strings.TrimPrefix(text, "export ")
		key, value, ok := strings.Cut(text, "=")
		if !ok || strings.TrimSpace(key) == "" {
			return fmt.Errorf("%s:%d: expected KEY=VALUE", path, line)
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		}
		if err := os.Setenv(strings.TrimSpace(key), value); err != nil {
			return err
		}
	}
	return s.Err()
}

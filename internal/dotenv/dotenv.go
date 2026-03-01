package dotenv

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
)

// LoadFile loads KEY=VALUE pairs from a dotenv-style file into the process
// environment. Existing environment variables are preserved.
func LoadFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("open env file %q: %w", path, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		line = strings.TrimPrefix(line, "export ")
		idx := strings.Index(line, "=")
		if idx <= 0 {
			continue
		}

		key := strings.TrimSpace(line[:idx])
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}

		val := strings.TrimSpace(line[idx+1:])
		if len(val) >= 2 {
			if strings.HasPrefix(val, `"`) && strings.HasSuffix(val, `"`) {
				val = val[1 : len(val)-1]
			} else if strings.HasPrefix(val, "'") && strings.HasSuffix(val, "'") {
				val = val[1 : len(val)-1]
			}
		}
		if err := os.Setenv(key, val); err != nil {
			return fmt.Errorf("set env %q from %q: %w", key, path, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan env file %q: %w", path, err)
	}
	return nil
}

package dotenv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFile_MissingFileIsNoop(t *testing.T) {
	t.Parallel()
	if err := LoadFile(filepath.Join(t.TempDir(), ".env")); err != nil {
		t.Fatalf("LoadFile missing file error: %v", err)
	}
}

func TestLoadFile_LoadsValuesAndPreservesExisting(t *testing.T) {
	tempDir := t.TempDir()
	envPath := filepath.Join(tempDir, ".env")
	content := "" +
		"# comment\n" +
		"FROM_FILE=loaded\n" +
		"QUOTED=\"hello world\"\n" +
		"export EXPORTED=ok\n" +
		"EXISTING=from_file\n"
	if err := os.WriteFile(envPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write env file: %v", err)
	}

	t.Setenv("EXISTING", "already_set")

	if err := LoadFile(envPath); err != nil {
		t.Fatalf("LoadFile error: %v", err)
	}

	if got := os.Getenv("FROM_FILE"); got != "loaded" {
		t.Fatalf("FROM_FILE=%q, want %q", got, "loaded")
	}
	if got := os.Getenv("QUOTED"); got != "hello world" {
		t.Fatalf("QUOTED=%q, want %q", got, "hello world")
	}
	if got := os.Getenv("EXPORTED"); got != "ok" {
		t.Fatalf("EXPORTED=%q, want %q", got, "ok")
	}
	if got := os.Getenv("EXISTING"); got != "already_set" {
		t.Fatalf("EXISTING=%q, want existing value preserved", got)
	}
}

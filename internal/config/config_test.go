package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigFileWithEnvOverrides(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(`
server:
  base_url: "http://file.example.com"
  port: 9090
approval:
  timeout_minutes: 15
logging:
  level: "debug"
`), 0o600); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	t.Setenv("SCHEDLOCK_CONFIG_FILE", cfgPath)
	t.Setenv("SCHEDLOCK_SERVER_SECRET", "test-secret")
	t.Setenv("SCHEDLOCK_ENCRYPTION_KEY", "test-encryption")
	t.Setenv("SCHEDLOCK_AUTH_PASSWORD_HASH", "argon2id$fake")

	t.Setenv("SCHEDLOCK_SERVER_PORT", "8081")
	t.Setenv("SCHEDLOCK_BASE_URL", "http://env.example.com")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.Server.Port != 8081 {
		t.Fatalf("expected env override port 8081, got %d", cfg.Server.Port)
	}
	if cfg.Server.BaseURL != "http://env.example.com" {
		t.Fatalf("expected env override base_url, got %s", cfg.Server.BaseURL)
	}
	if cfg.Approval.TimeoutMinutes != 15 {
		t.Fatalf("expected approval timeout 15, got %d", cfg.Approval.TimeoutMinutes)
	}
	if cfg.Logging.Level != "debug" {
		t.Fatalf("expected logging level debug, got %s", cfg.Logging.Level)
	}
	if cfg.Google.RedirectURI != "http://env.example.com/oauth/callback" {
		t.Fatalf("unexpected redirect uri: %s", cfg.Google.RedirectURI)
	}
}

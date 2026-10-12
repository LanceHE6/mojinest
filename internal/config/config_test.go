package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaults(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.Listen != ":18642" || c.Storage != "local" || c.S3.Region != "auto" {
		t.Errorf("defaults = %+v", c)
	}
	if c.Tagger.Enabled {
		t.Error("tagger must be off by default")
	}
	if c.Tagger.TimeoutSec != 120 || c.Tagger.MaxConcurrent != 3 {
		t.Errorf("tagger defaults = %+v", c.Tagger)
	}
}

func TestSaveLoadRoundTripAndPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.json")
	c := &Config{
		Listen: ":9000", DataDir: "/srv/moji", Token: "tok-123", Storage: "s3",
		S3:     S3Config{Endpoint: "https://r2.example", Bucket: "b", AccessKey: "ak", SecretKey: "sk", PathStyle: true},
		Tagger: TagConfig{Enabled: true, BaseURL: "https://ark.example/v3", APIKey: "k", Model: "m"},
	}
	if err := c.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("config with secrets must be 0600, got %v", info.Mode().Perm())
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Token != "tok-123" || got.S3.Bucket != "b" || !got.S3.PathStyle || !got.Tagger.Enabled {
		t.Errorf("roundtrip = %+v / %+v", got, got.S3)
	}
}

func TestEnvOverrides(t *testing.T) {
	t.Setenv("MOJINEST_LISTEN", ":19999")
	t.Setenv("MOJINEST_DATA_DIR", "/tmp/envdata")
	t.Setenv("MOJINEST_TOKEN", "env-token")
	t.Setenv("MOJINEST_STORAGE", "s3")
	t.Setenv("MOJINEST_S3_ENDPOINT", "https://s3.example")
	t.Setenv("MOJINEST_S3_BUCKET", "envbucket")
	t.Setenv("MOJINEST_TAGGER_BASE_URL", "https://vl.example/v1")
	t.Setenv("MOJINEST_TAGGER_MODEL", "vl-env")
	t.Setenv("MOJINEST_TAGGER_API_KEY", "env-key")

	c, err := Load(filepath.Join(t.TempDir(), "none.json"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.Listen != ":19999" || c.DataDir != "/tmp/envdata" || c.Token != "env-token" || c.Storage != "s3" {
		t.Errorf("env overrides missed: %+v", c)
	}
	if c.S3.Endpoint != "https://s3.example" || c.S3.Bucket != "envbucket" {
		t.Errorf("s3 env = %+v", c.S3)
	}
	if !c.Tagger.Enabled || c.Tagger.APIKey != "env-key" || c.Tagger.Model != "vl-env" {
		t.Errorf("tagger env = %+v (an api key implies enabled)", c.Tagger)
	}

	// explicit opt-out wins over an api key
	t.Setenv("MOJINEST_TAGGER_ENABLED", "false")
	c2, _ := Load(filepath.Join(t.TempDir(), "none.json"))
	if c2.Tagger.Enabled {
		t.Error("MOJINEST_TAGGER_ENABLED=false should disable tagging")
	}
}

func TestBadConfigFileIsReported(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Error("expected a parse error for malformed config")
	}
}

func TestPathsAndDirs(t *testing.T) {
	dir := t.TempDir()
	c := &Config{DataDir: filepath.Join(dir, "data")}
	if err := c.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}
	for _, p := range []string{c.DataDir, c.FilesDir(), c.ThumbDir()} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("missing dir %s: %v", p, err)
		}
	}
	if filepath.Base(c.DBPath()) != "mojinest_local.db" {
		t.Errorf("db path = %s", c.DBPath())
	}
}

func TestDefaultPathRespectsEnv(t *testing.T) {
	t.Setenv("MOJINEST_CONFIG", "/tmp/custom-config.json")
	if got := DefaultPath(); got != "/tmp/custom-config.json" {
		t.Errorf("DefaultPath = %s", got)
	}
}

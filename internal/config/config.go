// Package config holds Mojinest runtime configuration.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type S3Config struct {
	Endpoint   string `json:"endpoint"`
	Region     string `json:"region"`
	Bucket     string `json:"bucket"`
	AccessKey  string `json:"access_key"`
	SecretKey  string `json:"secret_key"`
	PathStyle  bool   `json:"path_style"`
	PublicBase string `json:"public_base"`
}

type TagConfig struct {
	Enabled       bool     `json:"enabled"`
	BaseURL       string   `json:"base_url"`
	APIKey        string   `json:"api_key"`
	Model         string   `json:"model"`
	TimeoutSec    int      `json:"timeout_sec"`
	MaxConcurrent int      `json:"max_concurrent"`
	Prompt        string   `json:"prompt"`
	Categories    []string `json:"categories"`
}

type Config struct {
	Listen        string    `json:"listen"`
	DataDir       string    `json:"data_dir"`
	Token         string    `json:"token"`
	PublicBaseURL string    `json:"public_base_url"`
	Storage       string    `json:"storage_backend"` // local | s3
	S3            S3Config  `json:"s3"`
	Tagger        TagConfig `json:"tagger"`
}

// DefaultPath returns the config file path (override with MOJINEST_CONFIG).
func DefaultPath() string {
	if p := os.Getenv("MOJINEST_CONFIG"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "config.json"
	}
	return filepath.Join(home, ".config", "mojinest", "config.json")
}

func defaults() *Config {
	return &Config{
		Listen:  ":18642",
		DataDir: "data",
		Storage: "local",
		S3:      S3Config{Region: "auto"},
		Tagger: TagConfig{
			Model:         "gemini-3.8-flash-medium",
			TimeoutSec:    120,
			MaxConcurrent: 3,
			Categories: []string{
				"沙雕搞笑", "可爱萌系", "阴阳吐槽", "无语尴尬", "开心点赞", "悲伤emo", "愤怒暴躁", "问号疑惑",
			},
		},
	}
}

// Load reads the config file if present, then applies env overrides.
func Load(path string) (*Config, error) {
	c := defaults()
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, c); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	envStr(&c.Listen, "MOJINEST_LISTEN")
	envStr(&c.DataDir, "MOJINEST_DATA_DIR")
	envStr(&c.Token, "MOJINEST_TOKEN")
	envStr(&c.PublicBaseURL, "MOJINEST_PUBLIC_BASE_URL")
	envStr(&c.Storage, "MOJINEST_STORAGE")
	envStr(&c.S3.Endpoint, "MOJINEST_S3_ENDPOINT")
	envStr(&c.S3.Region, "MOJINEST_S3_REGION")
	envStr(&c.S3.Bucket, "MOJINEST_S3_BUCKET")
	envStr(&c.S3.AccessKey, "MOJINEST_S3_ACCESS_KEY")
	envStr(&c.S3.SecretKey, "MOJINEST_S3_SECRET_KEY")
	envStr(&c.S3.PublicBase, "MOJINEST_S3_PUBLIC_BASE")
	envStr(&c.Tagger.BaseURL, "MOJINEST_TAGGER_BASE_URL")
	envStr(&c.Tagger.Model, "MOJINEST_TAGGER_MODEL")
	envStr(&c.Tagger.Prompt, "MOJINEST_TAGGER_PROMPT")
	if v := os.Getenv("MOJINEST_TAGGER_API_KEY"); v != "" {
		c.Tagger.APIKey = v
		c.Tagger.Enabled = true
	}
	if v := os.Getenv("MOJINEST_TAGGER_ENABLED"); v == "0" || v == "false" {
		c.Tagger.Enabled = false
	}
	if c.Tagger.Enabled && os.Getenv("MOJINEST_TAGGER_BASE_URL") == "" && c.Tagger.BaseURL == "" && c.Tagger.APIKey == "" {
		c.Tagger.Enabled = false
	}
	return c, nil
}

func envStr(dst *string, key string) {
	if v := os.Getenv(key); v != "" {
		*dst = v
	}
}

// Save writes the config file with 0600 (it may hold tokens/keys).
func (c *Config) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

func (c *Config) FilesDir() string { return filepath.Join(c.DataDir, "files") }
func (c *Config) ThumbDir() string { return filepath.Join(c.DataDir, "thumbs") }
func (c *Config) DBPath() string {
	if c.Storage == "s3" {
		return filepath.Join(c.DataDir, "mojinest_r3.db")
	}
	return filepath.Join(c.DataDir, "mojinest_local.db")
}

func (c *Config) EnsureDirs() error {
	for _, d := range []string{c.DataDir, c.FilesDir(), c.ThumbDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

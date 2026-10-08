// Command mojinest runs the Mojinest sticker library server.
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"mojinest/internal/api"
	"mojinest/internal/config"
	"mojinest/internal/db"
	"mojinest/internal/repo"
	"mojinest/internal/store"
	"mojinest/internal/webui"
	"mojinest/internal/worker"
)

// randomToken returns a 32-byte URL-safe token for -init.
func randomToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func main() {
	cfgPath := flag.String("config", config.DefaultPath(), "path to config.json")
	dataDir := flag.String("data-dir", "", "override data directory")
	listen := flag.String("listen", "", "override listen address, e.g. :8642")
	storageBackend := flag.String("storage", "", "local | s3")
	tagBase := flag.String("tagger-base-url", "", "OpenAI-compatible vision API base URL")
	tagKey := flag.String("tagger-api-key", "", "vision API key")
	tagModel := flag.String("tagger-model", "", "vision model name")
	debug := flag.Bool("debug", false, "verbose logs")
	initCfg := flag.Bool("init", false, "write the config file and exit")
	flag.Parse()

	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Error("load config", "err", err, "path", *cfgPath)
		os.Exit(1)
	}
	if *dataDir != "" {
		cfg.DataDir = *dataDir
	}
	if *listen != "" {
		cfg.Listen = *listen
	}
	if *storageBackend != "" {
		cfg.Storage = *storageBackend
	}
	if *tagBase != "" {
		cfg.Tagger.BaseURL = *tagBase
	}
	if *tagKey != "" {
		cfg.Tagger.APIKey, cfg.Tagger.Enabled = *tagKey, true
	}
	if *tagModel != "" {
		cfg.Tagger.Model = *tagModel
	}
	if *initCfg {
		if cfg.Token == "" {
			tok, err := randomToken()
			if err != nil {
				log.Error("generate token", "err", err)
				os.Exit(1)
			}
			cfg.Token = tok
		}
		if err := cfg.Save(*cfgPath); err != nil {
			log.Error("save config", "err", err)
			os.Exit(1)
		}
		log.Info("config written", "path", *cfgPath)
		fmt.Printf("配置已写入 %s\n访问令牌: %s\n启动: mojinest -config %s\n", *cfgPath, cfg.Token, *cfgPath)
		return
	}
	if err := cfg.EnsureDirs(); err != nil {
		log.Error("create data dirs", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	blobs, err := buildStore(ctx, cfg, log)
	if err != nil {
		log.Error("storage", "err", err)
		os.Exit(1)
	}
	log.Info("storage ready", "backend", blobs.Kind())

	sqlDB, err := db.Open(cfg.DBPath())
	if err != nil {
		log.Error("open database", "err", err)
		os.Exit(1)
	}
	defer sqlDB.Close()
	rp := repo.New(sqlDB)

	if !webui.Built() {
		log.Warn("frontend build missing: only /api/* is served")
	}

	w := worker.New(rp, blobs, cfg, log)
	go w.Run(ctx)

	srv := api.New(rp, blobs, cfg, log)
	httpSrv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
		// Uploads can be large; no global write timeout.
		IdleTimeout: 120 * time.Second,
	}

	go func() {
		log.Info("mojinest listening", "addr", cfg.Listen, "data", cfg.DataDir,
			"auth", cfg.Token != "", "tagger", cfg.Tagger.Enabled)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
}

func buildStore(ctx context.Context, cfg *config.Config, log *slog.Logger) (store.Store, error) {
	if cfg.Storage == "s3" {
		return store.NewS3(ctx, store.S3Options{
			Endpoint:   cfg.S3.Endpoint,
			Region:     cfg.S3.Region,
			Bucket:     cfg.S3.Bucket,
			AccessKey:  cfg.S3.AccessKey,
			SecretKey:  cfg.S3.SecretKey,
			PathStyle:  cfg.S3.PathStyle,
			PublicBase: cfg.S3.PublicBase,
		})
	}
	return store.NewLocal(cfg.DataDir)
}

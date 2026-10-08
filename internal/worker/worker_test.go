package worker

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mojinest/internal/config"
	"mojinest/internal/db"
	"mojinest/internal/repo"
	"mojinest/internal/store"
)

// visionStub answers /chat/completions like an OpenAI-compatible VL model.
func visionStub(t *testing.T, calls *int32, failFirst bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(calls, 1)
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			w.WriteHeader(404)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "data:image/png;base64,") {
			t.Errorf("request must carry a PNG data URL, got %.200s", body)
		}
		if failFirst && n == 1 {
			w.WriteHeader(500)
			_, _ = w.Write([]byte(`{"error":{"message":"model overloaded"}}`))
			return
		}
		content := "```json\n" + `{"description":"测试描述：一只猫在笑","ocr":"哈哈哈","tags":["猫","笑","沙雕"]}` + "\n```"
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": content}}},
		})
	}))
}

func newWorker(t *testing.T, baseURL string) (*Worker, *repo.Repo) {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{DataDir: dir, Storage: "local", Tagger: config.TagConfig{
		Enabled: baseURL != "", BaseURL: baseURL, APIKey: "k", Model: "test-vl", TimeoutSec: 5, MaxConcurrent: 1,
	}}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("dirs: %v", err)
	}
	blobs, err := store.NewLocal(cfg.DataDir)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	sqlDB, err := db.Open(filepath.Join(dir, "w.db"))
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	r := repo.New(sqlDB)
	w := New(r, blobs, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	w.retryDelay = 10 * time.Millisecond
	w.parkDelay = 10 * time.Millisecond
	return w, r
}

func seedItem(t *testing.T, r *repo.Repo, blobs store.Store) *repo.Item {
	t.Helper()
	ctx := context.Background()
	sha := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if err := blobs.Put(ctx, store.KeyFor(sha, "png"), strings.NewReader(string(gifAsPNG(t))), 0, "image/png"); err != nil {
		t.Fatalf("put blob: %v", err)
	}
	it, _, err := r.CreateItem(ctx, repo.CreateItemInput{
		File: repo.NewFile{Sha256: sha, Ext: "png", MIME: "image/png", Size: 100, Width: 8, Height: 8, Frames: 1},
		Name: "seed", Tags: []string{"手工标签"},
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	return it
}

func TestWorkerTagsItemAndKeepsManualTags(t *testing.T) {
	var calls int32
	stub := visionStub(t, &calls, false)
	defer stub.Close()

	w, r := newWorker(t, stub.URL+"/v1")
	it := seedItem(t, r, w.Store)

	job, err := r.ClaimJob(context.Background())
	if err != nil || job == nil {
		t.Fatalf("claim: %v %v", job, err)
	}
	w.handle(context.Background(), job)

	got, err := r.GetItem(context.Background(), it.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.AIDescription != "测试描述：一只猫在笑" || got.AIOCR != "哈哈哈" {
		t.Errorf("ai fields = %q / %q", got.AIDescription, got.AIOCR)
	}
	names := map[string]string{}
	for _, tg := range got.Tags {
		names[tg.Name] = tg.Source
	}
	if names["手工标签"] != "manual" {
		t.Errorf("manual tag lost: %+v", got.Tags)
	}
	for _, want := range []string{"猫", "笑", "沙雕"} {
		if names[want] != "ai" {
			t.Errorf("missing ai tag %q in %+v", want, got.Tags)
		}
	}
	stats, _ := r.JobStats(context.Background())
	if stats["done"] != 1 {
		t.Errorf("job stats = %+v", stats)
	}
}

func TestWorkerRetriesThenSucceeds(t *testing.T) {
	var calls int32
	stub := visionStub(t, &calls, true) // first call fails
	defer stub.Close()

	w, r := newWorker(t, stub.URL+"/v1")
	w.retryDelay = 10 * time.Millisecond
	seedItem(t, r, w.Store)

	ctx := context.Background()
	job, _ := r.ClaimJob(ctx)
	if job == nil {
		t.Fatal("no job")
	}
	// first attempt: model errors → job goes back to pending with attempts=1
	w.handle(ctx, job)
	stats, _ := r.JobStats(ctx)
	if stats["pending"] != 1 {
		t.Fatalf("expected requeue after failure, stats=%+v", stats)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("stub calls = %d, want 1", got)
	}
	job2, _ := r.ClaimJob(ctx)
	if job2 == nil || job2.Attempts != 2 {
		t.Fatalf("second claim = %+v", job2)
	}
	w.handle(ctx, job2)
	stats, _ = r.JobStats(ctx)
	if stats["done"] != 1 || stats["pending"] != 0 {
		t.Errorf("stats after retry = %+v", stats)
	}
}

func TestWorkerGivesUpAfterThreeAttempts(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer stub.Close()

	w, r := newWorker(t, stub.URL+"/v1")
	seedItem(t, r, w.Store)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		job, _ := r.ClaimJob(ctx)
		if job == nil {
			t.Fatalf("no job on attempt %d", i+1)
		}
		w.handle(ctx, job)
	}
	stats, _ := r.JobStats(ctx)
	if stats["failed"] != 1 || stats["pending"] != 0 {
		t.Errorf("stats = %+v, want failed=1 pending=0", stats)
	}
	// retry endpoint path: failed jobs can be requeued
	n, _ := r.ResetFailedForRetry(ctx)
	if n != 1 {
		t.Errorf("requeued = %d", n)
	}
}

func TestWorkerParksQueueWhenTaggerDisabled(t *testing.T) {
	w, r := newWorker(t, "") // tagger disabled
	w.parkDelay = 20 * time.Millisecond
	w.retryDelay = 20 * time.Millisecond
	seedItem(t, r, w.Store)
	ctx := context.Background()

	job, _ := r.ClaimJob(ctx)
	if job == nil {
		t.Fatal("no job")
	}
	done := make(chan struct{})
	go func() { w.handle(ctx, job); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handle did not return")
	}
	stats, _ := r.JobStats(context.Background())
	if stats["pending"] != 1 || stats["done"] != 0 || stats["failed"] != 0 {
		t.Errorf("stats = %+v, want the job parked as pending", stats)
	}
}

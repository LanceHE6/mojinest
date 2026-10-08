// Package worker runs the background pipeline: AI tagging jobs.
package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"time"

	"mojinest/internal/config"
	"mojinest/internal/imageutil"
	"mojinest/internal/repo"
	"mojinest/internal/store"
	"mojinest/internal/tagger"
)

// Worker drains the job queue.
type Worker struct {
	Repo    *repo.Repo
	Store   store.Store
	Cfg     *config.Config
	Log     *slog.Logger
	backoff time.Duration
	// parkDelay is how long an unconfigured tagger parks the queue before the
	// job is released; overridable in tests.
	parkDelay  time.Duration
	retryDelay time.Duration
}

// New builds a worker.
func New(r *repo.Repo, s store.Store, cfg *config.Config, log *slog.Logger) *Worker {
	return &Worker{
		Repo: r, Store: s, Cfg: cfg, Log: log,
		parkDelay:  30 * time.Second,
		retryDelay: 10 * time.Second,
	}
}

// Run polls forever until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		for {
			job, err := w.Repo.ClaimJob(ctx)
			if err != nil {
				w.Log.Error("claim job", "err", err)
				break
			}
			if job == nil {
				break
			}
			w.handle(ctx, job)
		}
	}
}

// Enqueue adds jobs for items that have never been analysed (used on startup).
func (w *Worker) handle(ctx context.Context, job *repo.Job) {
	if !w.Cfg.Tagger.Enabled {
		// Park the job: release it back to the queue after a pause so an
		// unconfigured instance neither mislabels nor spams the log.
		select {
		case <-ctx.Done():
		case <-time.After(w.parkDelay):
		}
		_ = w.Repo.RequeueJob(ctx, job.ID, "tagger disabled")
		return
	}
	item, err := w.Repo.GetItem(ctx, job.ItemID)
	if err != nil {
		_ = w.Repo.FinishJob(ctx, job.ID, err)
		return
	}
	data, err := w.readBlob(ctx, item)
	if err != nil {
		_ = w.Repo.FinishJob(ctx, job.ID, err)
		return
	}
	png, err := imageutil.FirstFramePNG(data, item.MIME)
	if err == nil {
		// Shrink for the model: smaller payload, fewer tokens.
		if img, derr := imageutil.DownscalePNG(png, 512); derr == nil {
			png = img
		}
	}
	if err != nil {
		_ = w.Repo.FinishJob(ctx, job.ID, err)
		return
	}
	tg := tagger.New(w.Cfg.Tagger.BaseURL, w.Cfg.Tagger.APIKey, w.Cfg.Tagger.Model,
		w.Cfg.Tagger.Prompt, time.Duration(w.Cfg.Tagger.TimeoutSec)*time.Second)
	res, err := tg.Analyze(ctx, png)
	if err != nil {
		w.Log.Warn("tagger failed", "item", item.ID, "err", err)
		if job.Attempts >= 3 {
			_ = w.Repo.FinishJob(ctx, job.ID, err)
			return
		}
		if qerr := w.Repo.RequeueJob(ctx, job.ID, err.Error()); qerr != nil {
			w.Log.Error("requeue job", "err", qerr)
		}
		select {
		case <-ctx.Done():
		case <-time.After(w.retryDelay):
		}
		return
	}
	if err := w.Repo.SetAI(ctx, item.ID, res.Description, res.OCR, res.Model); err != nil {
		_ = w.Repo.FinishJob(ctx, job.ID, err)
		return
	}
	for _, t := range res.Tags {
		if err := w.Repo.AddTag(ctx, item.ID, t, "ai"); err != nil && !errors.Is(err, repo.ErrNotFound) {
			w.Log.Warn("add ai tag", "item", item.ID, "tag", t, "err", err)
		}
	}
	if err := w.Repo.FinishJob(ctx, job.ID, nil); err != nil {
		w.Log.Error("finish job", "err", err)
	}
	w.Log.Info("tagged", "item", item.ID, "tags", len(res.Tags))
}

func (w *Worker) readBlob(ctx context.Context, item *repo.Item) ([]byte, error) {
	rc, err := w.Store.Get(ctx, store.KeyFor(item.Sha256, item.Ext))
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, 64<<20))
	if err != nil {
		return nil, err
	}
	return data, nil
}

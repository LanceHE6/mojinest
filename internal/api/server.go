// Package api implements the Mojinest HTTP API.
package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"mojinest/internal/config"
	"mojinest/internal/db"
	"mojinest/internal/imageutil"
	"mojinest/internal/repo"
	"mojinest/internal/store"
	"mojinest/internal/webui"
)

const maxUpload = 32 << 20 // per file

// Server wires the repo, blob store and configuration into HTTP handlers.
type Server struct {
	Repo  *repo.Repo
	Store store.Store
	Cfg   *config.Config
	Log   *slog.Logger

	mu      sync.RWMutex
	limiter *limiter
	cookie  string
	web     fs.FS
}

// New builds the API server.
func New(r *repo.Repo, s store.Store, cfg *config.Config, log *slog.Logger) *Server {
	cookie := "mojinest_token"
	if cfg.Token != "" {
		sum := sha256.Sum256([]byte("mojinest/" + cfg.Token))
		cookie = "mojinest_" + hex.EncodeToString(sum[:4])
	}
	sub, err := webui.FS()
	if err != nil {
		log.Warn("webui fs", "err", err)
	}
	return &Server{
		Repo:    r,
		Store:   s,
		Cfg:     cfg,
		Log:     log,
		limiter: newLimiter(2400, 400),
		cookie:  cookie,
		web:     sub,
	}
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/auth/logout", s.handleLogout)
	mux.HandleFunc("GET /api/auth/me", s.authed(s.handleMe))

	mux.HandleFunc("GET /api/items", s.authed(s.handleListItems))
	mux.HandleFunc("POST /api/items", s.authed(s.handleUpload))
	mux.HandleFunc("PATCH /api/items/{id}", s.authed(s.handlePatchItem))
	mux.HandleFunc("POST /api/items/{id}/use", s.authed(s.handleUse))
	mux.HandleFunc("POST /api/items/{id}/trash", s.authed(s.handleTrash))
	mux.HandleFunc("POST /api/items/{id}/restore", s.authed(s.handleRestore))
	mux.HandleFunc("DELETE /api/items/{id}", s.authed(s.handleHardDelete))
	mux.HandleFunc("POST /api/items/{id}/tags", s.authed(s.handleAddTag))
	mux.HandleFunc("DELETE /api/items/{id}/tags/{name}", s.authed(s.handleRemoveTag))
	mux.HandleFunc("PUT /api/items/{id}/collections", s.authed(s.handleSetCollections))
	mux.HandleFunc("POST /api/items/batch", s.authed(s.handleBatch))

	mux.HandleFunc("GET /api/tags", s.authed(s.handleTags))
	mux.HandleFunc("GET /api/collections", s.authed(s.handleCollections))
	mux.HandleFunc("POST /api/collections", s.authed(s.handleCreateCollection))
	mux.HandleFunc("PATCH /api/collections/{id}", s.authed(s.handleRenameCollection))
	mux.HandleFunc("DELETE /api/collections/{id}", s.authed(s.handleDeleteCollection))

	mux.HandleFunc("GET /api/stats", s.authed(s.handleStats))
	mux.HandleFunc("GET /api/jobs", s.authed(s.handleJobs))
	mux.HandleFunc("POST /api/jobs/retry", s.authed(s.handleRetryJobs))
	mux.HandleFunc("POST /api/trash/empty", s.authed(s.handleEmptyTrash))

	mux.HandleFunc("GET /api/config/storage", s.authed(s.handleGetStorageConfig))
	mux.HandleFunc("POST /api/config/storage", s.authed(s.handleSetStorageConfig))

	mux.HandleFunc("GET /api/files/{sha}", s.authed(s.handleFile))
	mux.HandleFunc("GET /api/thumbs/{sha}", s.authed(s.handleThumb))
	mux.HandleFunc("GET /api/export", s.authed(s.handleExport))

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "kind": s.Store.Kind()})
	})

	mux.Handle("/", s.staticHandler())
	return s.withMiddleware(mux)
}

func (s *Server) withMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 允许 Tauri 客户端及局域网前端跨域请求
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		if !s.limiter.allow(clientIP(r)) {
			writeErr(w, http.StatusTooManyRequests, "请求过于频繁，请稍后再试")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead &&
			strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/auth/login" {
			// Block cross-site writes (CSRF): only same-origin or no Origin or localhost/tauri.
			if o := r.Header.Get("Origin"); o != "" && !sameOrigin(o, r.Host) && !strings.Contains(o, "tauri://") && !strings.Contains(o, "localhost") {
				writeErr(w, http.StatusForbidden, "cross-origin request denied")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func sameOrigin(origin, host string) bool {
	origin = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(origin, "https://"), "http://"), "/")
	return origin == host
}

// --- auth ---

func (s *Server) authorized(r *http.Request) bool {
	if s.Cfg.Token == "" {
		return true // open instance (localhost-only deployments)
	}
	if c, err := r.Cookie(s.cookie); err == nil && c.Value != "" {
		if subtle.ConstantTimeCompare([]byte(c.Value), []byte(s.Cfg.Token)) == 1 {
			return true
		}
	}
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		if subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(h, "Bearer ")), []byte(s.Cfg.Token)) == 1 {
			return true
		}
	}
	if t := r.URL.Query().Get("token"); t != "" {
		if subtle.ConstantTimeCompare([]byte(t), []byte(s.Cfg.Token)) == 1 {
			return true
		}
	}
	return false
}

func (s *Server) authed(next func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r) {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r)
	}
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if s.Cfg.Token == "" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "note": "token not configured"})
		return
	}
	if subtle.ConstantTimeCompare([]byte(body.Token), []byte(s.Cfg.Token)) != 1 {
		time.Sleep(300 * time.Millisecond) // slow down guessing
		writeErr(w, http.StatusUnauthorized, "token 不正确")
		return
	}
	secure := r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	http.SetCookie(w, &http.Cookie{
		Name: s.cookie, Value: s.Cfg.Token, Path: "/", HttpOnly: true,
		Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: 60 * 60 * 24 * 180,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: s.cookie, Value: "", Path: "/", MaxAge: -1})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"user": "owner", "id": 1, "storage": s.Store.Kind()})
}

// --- items ---

func (s *Server) handleListItems(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := repo.Filter{
		Q:            q.Get("q"),
		FavoriteOnly: q.Get("favorite") == "1",
		Platform:     q.Get("platform"),
		AnimatedOnly: q.Get("animated") == "1",
		Sort:         q.Get("sort"),
		Limit:        atoiDefault(q.Get("limit"), 60),
		Offset:       atoiDefault(q.Get("offset"), 0),
	}
	if v := q.Get("tags"); v != "" {
		for _, t := range strings.Split(v, ",") {
			if t = strings.TrimSpace(t); t != "" {
				f.Tags = append(f.Tags, t)
			}
		}
	}
	if v := q.Get("collection"); v != "" {
		f.Collection = int64(atoiDefault(v, 0))
	}
	f.Deleted = q.Get("trash") == "1"

	items, total, err := s.Repo.ListItems(r.Context(), f)
	if err != nil {
		s.fail(w, "list items", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(maxUpload * 4); err != nil {
		writeErr(w, http.StatusBadRequest, "无法解析上传内容："+err.Error())
		return
	}
	tags := splitCSV(r.FormValue("tags"))
	platform := r.FormValue("platform")
	hint := r.FormValue("source_hint")
	var collections []int64
	for _, v := range splitCSV(r.FormValue("collections")) {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			collections = append(collections, n)
		}
	}

	files := r.MultipartForm.File["file"]
	if len(files) == 0 {
		writeErr(w, http.StatusBadRequest, "没有收到文件")
		return
	}

	type result struct {
		Item *repo.Item `json:"item,omitempty"`
		Err  string     `json:"error,omitempty"`
	}
	items := []*repo.Item{}
	skipped := 0
	errorsOut := []result{}

	for _, fh := range files {
		if fh.Size > maxUpload {
			errorsOut = append(errorsOut, result{Err: fh.Filename + ": 文件超过 32MB"})
			skipped++
			continue
		}
		f, err := fh.Open()
		if err != nil {
			errorsOut = append(errorsOut, result{Err: fh.Filename + ": " + err.Error()})
			skipped++
			continue
		}
		data, err := io.ReadAll(io.LimitReader(f, maxUpload+1))
		f.Close()
		if err != nil {
			errorsOut = append(errorsOut, result{Err: fh.Filename + ": " + err.Error()})
			skipped++
			continue
		}
		it, err := s.ingest(r.Context(), data, ingestInput{
			Name:        strings.TrimSpace(strings.TrimSuffix(fh.Filename, path.Ext(fh.Filename))),
			Platform:    platform,
			Hint:        hint,
			Tags:        tags,
			Collections: collections,
		})
		if err != nil {
			if errors.Is(err, errDuplicate) {
				skipped++
				continue
			}
			errorsOut = append(errorsOut, result{Err: fh.Filename + ": " + err.Error()})
			skipped++
			continue
		}
		items = append(items, it)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"items": items, "skipped": skipped, "errors": errorsOut,
	})
}

var errDuplicate = errors.New("duplicate")

type ingestInput struct {
	Name        string
	Platform    string
	Hint        string
	Tags        []string
	Collections []int64
}

// ingest validates, stores and registers one uploaded image.
func (s *Server) ingest(ctx context.Context, data []byte, in ingestInput) (*repo.Item, error) {
	if len(data) == 0 {
		return nil, errors.New("空文件")
	}
	mimeType := imageutil.Sniff(data)
	if mimeType == "" {
		return nil, errors.New("不是支持的图片格式（png/jpg/gif/webp）")
	}
	meta, err := imageutil.Inspect(data, mimeType)
	if err != nil {
		return nil, errors.New("图片损坏或无法解码：" + err.Error())
	}
	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])
	ext := imageutil.Ext(mimeType)

	key := store.KeyFor(sha, ext)
	exists, err := s.Store.Exists(ctx, key)
	if err != nil {
		s.Log.Warn("store exists check", "err", err)
	}
	if !exists {
		if err := s.Store.Put(ctx, key, bytes.NewReader(data), int64(len(data)), mimeType); err != nil {
			return nil, err
		}
	}
	if err := s.writeThumb(ctx, data, mimeType, sha); err != nil {
		s.Log.Warn("thumb", "sha", sha, "err", err)
	}

	item, existed, err := s.Repo.CreateItem(ctx, repo.CreateItemInput{
		File: repo.NewFile{
			Sha256: sha, Ext: ext, MIME: mimeType, Size: int64(len(data)),
			Width: meta.Width, Height: meta.Height, Animated: meta.Animated, Frames: meta.Frames,
		},
		Name: in.Name, SourcePlatform: in.Platform, SourceHint: in.Hint,
		Tags: in.Tags, Collections: in.Collections,
	})
	if err != nil {
		return nil, err
	}
	if existed {
		return nil, errDuplicate
	}
	return item, nil
}

func (s *Server) writeThumb(ctx context.Context, data []byte, mimeType, sha string) error {
	thumb, ext, ct, err := imageutil.Thumb(data, mimeType, 320)
	if err != nil {
		return err
	}
	return s.Store.Put(ctx, store.ThumbKeyFor(sha, ext), bytes.NewReader(thumb), int64(len(thumb)), ct)
}

func (s *Server) handlePatchItem(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var body struct {
		Name           *string `json:"name"`
		Note           *string `json:"note"`
		SourcePlatform *string `json:"source_platform"`
		SourceHint     *string `json:"source_hint"`
		Favorite       *bool   `json:"favorite"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if err := s.Repo.UpdateItem(r.Context(), id, repo.UpdateItemInput{
		Name: body.Name, Note: body.Note, SourcePlatform: body.SourcePlatform,
		SourceHint: body.SourceHint, Favorite: body.Favorite,
	}); err != nil {
		s.fail(w, "update item", err)
		return
	}
	s.respondItem(w, r, id)
}

func (s *Server) handleUse(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	if err := s.Repo.UseItem(r.Context(), id); err != nil {
		s.fail(w, "use item", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleTrash(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	if err := s.Repo.TrashItem(r.Context(), id); err != nil {
		s.fail(w, "trash item", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	if err := s.Repo.RestoreItem(r.Context(), id); err != nil {
		s.fail(w, "restore item", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleHardDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	sha, drop, err := s.Repo.HardDelete(r.Context(), id)
	if err != nil {
		s.fail(w, "delete item", err)
		return
	}
	if drop {
		s.removeBlobs(r.Context(), sha)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) removeBlobs(ctx context.Context, sha string) {
	for _, ext := range []string{"jpg", "png"} {
		_ = s.Store.Delete(ctx, store.ThumbKeyFor(sha, ext))
	}
	for _, ext := range []string{"png", "jpg", "jpeg", "gif", "webp"} {
		_ = s.Store.Delete(ctx, store.KeyFor(sha, ext))
	}
}

func (s *Server) handleAddTag(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if err := s.Repo.AddTag(r.Context(), id, body.Name, "manual"); err != nil {
		s.fail(w, "add tag", err)
		return
	}
	s.respondItem(w, r, id)
}

func (s *Server) handleRemoveTag(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	name := r.PathValue("name")
	if err := s.Repo.RemoveTag(r.Context(), id, name); err != nil {
		s.fail(w, "remove tag", err)
		return
	}
	s.respondItem(w, r, id)
}

func (s *Server) handleSetCollections(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	var body struct {
		IDs []int64 `json:"ids"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if err := s.Repo.SetItemCollections(r.Context(), id, body.IDs); err != nil {
		s.fail(w, "set collections", err)
		return
	}
	s.respondItem(w, r, id)
}

// handleBatch runs one of: trash, restore, delete, favorite, tag, collection.
func (s *Server) handleBatch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action       string  `json:"action"`
		IDs          []int64 `json:"ids"`
		Tag          string  `json:"tag"`
		Favorite     *bool   `json:"favorite"`
		CollectionID int64   `json:"collection_id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if len(body.IDs) == 0 {
		writeErr(w, http.StatusBadRequest, "ids 为空")
		return
	}
	ctx := r.Context()
	for _, id := range body.IDs {
		var err error
		switch body.Action {
		case "trash":
			err = s.Repo.TrashItem(ctx, id)
		case "restore":
			err = s.Repo.RestoreItem(ctx, id)
		case "delete":
			var sha string
			var drop bool
			sha, drop, err = s.Repo.HardDelete(ctx, id)
			if err == nil && drop {
				s.removeBlobs(ctx, sha)
			}
		case "favorite":
			fav := true
			if body.Favorite != nil {
				fav = *body.Favorite
			}
			err = s.Repo.UpdateItem(ctx, id, repo.UpdateItemInput{Favorite: &fav})
		case "tag":
			err = s.Repo.AddTag(ctx, id, body.Tag, "manual")
		case "collection":
			err = s.Repo.AddItemsToCollection(ctx, []int64{id}, body.CollectionID)
		default:
			writeErr(w, http.StatusBadRequest, "未知 action: "+body.Action)
			return
		}
		if err != nil && !errors.Is(err, repo.ErrNotFound) {
			s.fail(w, "batch "+body.Action, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "count": len(body.IDs)})
}

func (s *Server) respondItem(w http.ResponseWriter, r *http.Request, id int64) {
	it, err := s.Repo.GetItem(r.Context(), id)
	if err != nil {
		s.fail(w, "get item", err)
		return
	}
	writeJSON(w, http.StatusOK, it)
}

// --- tags / collections ---

func (s *Server) handleTags(w http.ResponseWriter, r *http.Request) {
	tags, err := s.Repo.Tags(r.Context())
	if err != nil {
		s.fail(w, "list tags", err)
		return
	}
	writeJSON(w, http.StatusOK, tags)
}

func (s *Server) handleCollections(w http.ResponseWriter, r *http.Request) {
	cs, err := s.Repo.ListCollections(r.Context())
	if err != nil {
		s.fail(w, "list collections", err)
		return
	}
	writeJSON(w, http.StatusOK, cs)
}

func (s *Server) handleCreateCollection(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     string `json:"name"`
		ParentID *int64 `json:"parent_id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	id, err := s.Repo.CreateCollection(r.Context(), body.Name, body.ParentID)
	if err != nil {
		s.fail(w, "create collection", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (s *Server) handleRenameCollection(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if err := s.Repo.RenameCollection(r.Context(), id, body.Name); err != nil {
		s.fail(w, "rename collection", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleDeleteCollection(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	if err := s.Repo.DeleteCollection(r.Context(), id); err != nil {
		s.fail(w, "delete collection", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// --- stats / jobs ---

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	st, err := s.Repo.Stats(r.Context())
	if err != nil {
		s.fail(w, "stats", err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	st, err := s.Repo.JobStats(r.Context())
	if err != nil {
		s.fail(w, "job stats", err)
		return
	}
	out := map[string]any{
		"pending": st["pending"], "running": st["running"],
		"done": st["done"], "failed": st["failed"],
		"tagger_enabled": s.Cfg.Tagger.Enabled,
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleRetryJobs(w http.ResponseWriter, r *http.Request) {
	n, err := s.Repo.ResetFailedForRetry(r.Context())
	if err != nil {
		s.fail(w, "retry jobs", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"requeued": n})
}

func (s *Server) handleEmptyTrash(w http.ResponseWriter, r *http.Request) {
	blobs, err := s.Repo.EmptyTrash(r.Context())
	if err != nil {
		s.fail(w, "empty trash", err)
		return
	}
	for _, sha := range blobs {
		s.removeBlobs(r.Context(), sha)
	}
	writeJSON(w, http.StatusOK, map[string]any{"removed": len(blobs)})
}

// --- storage config ---

type StorageConfigResponse struct {
	Backend string          `json:"backend"`
	DataDir string          `json:"data_dir"`
	S3      config.S3Config `json:"s3"`
}

type UpdateStorageConfigRequest struct {
	Backend string          `json:"backend"` // local | s3
	DataDir string          `json:"data_dir"`
	S3      config.S3Config `json:"s3"`
}

func (s *Server) handleGetStorageConfig(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	resp := StorageConfigResponse{
		Backend: s.Cfg.Storage,
		DataDir: s.Cfg.DataDir,
		S3:      s.Cfg.S3,
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleSetStorageConfig(w http.ResponseWriter, r *http.Request) {
	var in UpdateStorageConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid payload")
		return
	}

	if in.Backend != "local" && in.Backend != "s3" {
		writeErr(w, http.StatusBadRequest, "backend must be 'local' or 's3'")
		return
	}

	// 动态创建并测试新的 Store
	var newStore store.Store
	if in.Backend == "local" {
		dir := in.DataDir
		if dir == "" {
			dir = "data"
		}
		ls, err := store.NewLocal(dir)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "failed to initialize local store: "+err.Error())
			return
		}
		newStore = ls
	} else {
		if in.S3.Bucket == "" {
			writeErr(w, http.StatusBadRequest, "s3: bucket is required")
			return
		}
		opts := store.S3Options{
			Endpoint:   in.S3.Endpoint,
			Region:     in.S3.Region,
			Bucket:     in.S3.Bucket,
			AccessKey:  in.S3.AccessKey,
			SecretKey:  in.S3.SecretKey,
			PathStyle:  in.S3.PathStyle,
			PublicBase: in.S3.PublicBase,
		}
		s3Store, err := store.NewS3(r.Context(), opts)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "failed to initialize s3 store: "+err.Error())
			return
		}
		newStore = s3Store
	}

	// 方案1：为不同存储空间独立绑定物理隔离的 SQLite 数据库
	targetCfg := *s.Cfg
	targetCfg.Storage = in.Backend
	if in.Backend == "local" && in.DataDir != "" {
		targetCfg.DataDir = in.DataDir
	}
	newDB, err := db.Open(targetCfg.DBPath())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to open isolated database: "+err.Error())
		return
	}
	newRepo := repo.New(newDB)

	s.mu.Lock()
	s.Cfg.Storage = in.Backend
	if in.Backend == "local" && in.DataDir != "" {
		s.Cfg.DataDir = in.DataDir
	}
	if in.Backend == "s3" {
		s.Cfg.S3 = in.S3
	}
	s.Store = newStore
	s.Repo = newRepo

	// 持久化保存配置文件
	cfgPath := os.Getenv("MOJINEST_CONFIG")
	if cfgPath == "" {
		cfgPath = config.DefaultPath()
	}
	_ = s.Cfg.Save(cfgPath)
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"message": "服务端存储引擎配置成功并已实时生效！",
		"backend": in.Backend,
	})
}

// --- blobs ---

func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	sha := r.PathValue("sha")
	ext := r.URL.Query().Get("ext")
	if !validSha(sha) {
		writeErr(w, http.StatusBadRequest, "bad sha")
		return
	}
	if ext == "" {
		ext = "png"
	}
	key := store.KeyFor(sha, ext)
	s.serveBlob(w, r, key, mime.TypeByExtension("."+ext), true)
}

func (s *Server) handleThumb(w http.ResponseWriter, r *http.Request) {
	sha := r.PathValue("sha")
	if !validSha(sha) {
		writeErr(w, http.StatusBadRequest, "bad sha")
		return
	}
	for _, cand := range []struct{ ext, ct string }{{"jpg", "image/jpeg"}, {"png", "image/png"}} {
		key := store.ThumbKeyFor(sha, cand.ext)
		ok, err := s.Store.Exists(r.Context(), key)
		if err == nil && ok {
			s.serveBlob(w, r, key, cand.ct, false)
			return
		}
	}
	// Fall back to the original when no thumbnail exists yet.
	for _, ext := range []string{"png", "jpg", "gif", "webp"} {
		key := store.KeyFor(sha, ext)
		if ok, err := s.Store.Exists(r.Context(), key); err == nil && ok {
			s.serveBlob(w, r, key, mime.TypeByExtension("."+ext), false)
			return
		}
	}
	writeErr(w, http.StatusNotFound, "not found")
}

func (s *Server) serveBlob(w http.ResponseWriter, r *http.Request, key, ct string, download bool) {
	if u, _ := s.Store.URL(r.Context(), key); u != "" && s.Cfg.S3.PublicBase != "" {
		http.Redirect(w, r, u, http.StatusFound)
		return
	}
	rc, err := s.Store.Get(r.Context(), key)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	defer rc.Close()
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	if download {
		w.Header().Set("Cache-Control", "private, max-age=86400")
	} else {
		w.Header().Set("Cache-Control", "private, max-age=604800")
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}

// --- export ---

// handleExport streams a JSON manifest of the whole library (for desktop
// clients and backups).
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	items, _, err := s.Repo.ListItems(r.Context(), repo.Filter{Limit: 300, Sort: "oldest"})
	if err != nil {
		s.fail(w, "export", err)
		return
	}
	tags, _ := s.Repo.Tags(r.Context())
	cols, _ := s.Repo.ListCollections(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"version": 1, "generated_at": time.Now().Unix(),
		"base": s.Cfg.PublicBaseURL, "storage": s.Store.Kind(),
		"items": items, "tags": tags, "collections": cols,
	})
}

// --- helpers ---

func (s *Server) fail(w http.ResponseWriter, what string, err error) {
	if errors.Is(err, repo.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	s.Log.Error(what, "err", err)
	writeErr(w, http.StatusInternalServerError, err.Error())
}

func (s *Server) staticHandler() http.Handler {
	fileServer := http.FileServer(http.FS(s.web))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeErr(w, http.StatusNotFound, "no such endpoint")
			return
		}
		if s.web == nil {
			writeErr(w, http.StatusServiceUnavailable, "frontend not built")
			return
		}
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(s.web, p); err != nil {
			// SPA fallback
			r = r.Clone(r.Context())
			r.URL.Path = "/"
		}
		fileServer.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": msg})
}

func pathID(r *http.Request) (int64, error) {
	return strconv.ParseInt(r.PathValue("id"), 10, 64)
}

func atoiDefault(s string, d int) int {
	if s == "" {
		return d
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return d
	}
	return n
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func validSha(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

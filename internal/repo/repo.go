// Package repo contains all SQL for Mojinest.
package repo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Repo struct{ DB *sql.DB }

func New(db *sql.DB) *Repo { return &Repo{DB: db} }

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

// TagRef is a tag plus how it got attached (manual | ai).
type TagRef struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

// Item is a sticker in the library.
type Item struct {
	ID             int64    `json:"id"`
	Sha256         string   `json:"sha256"`
	Ext            string   `json:"ext"`
	MIME           string   `json:"mime"`
	Size           int64    `json:"size"`
	Width          int      `json:"width"`
	Height         int      `json:"height"`
	Animated       bool     `json:"animated"`
	Frames         int      `json:"frames"`
	Name           string   `json:"name"`
	SourcePlatform string   `json:"source_platform"`
	SourceHint     string   `json:"source_hint"`
	Note           string   `json:"note"`
	Favorite       bool     `json:"favorite"`
	UseCount       int64    `json:"use_count"`
	LastUsedAt     int64    `json:"last_used_at"`
	CreatedAt      int64    `json:"created_at"`
	Tags           []TagRef `json:"tags"`
	Collections    []int64  `json:"collections"`
	AIDescription  string   `json:"ai_description"`
	AIOCR          string   `json:"ai_ocr"`
}

// NewFile describes a freshly uploaded blob.
type NewFile struct {
	Sha256   string
	Ext      string
	MIME     string
	Size     int64
	Width    int
	Height   int
	Animated bool
	Frames   int
}

// CreateItemInput is the payload for creating a library item.
type CreateItemInput struct {
	File           NewFile
	Name           string
	SourcePlatform string
	SourceHint     string
	Note           string
	Tags           []string
	Collections    []int64
}

// CreateItem upserts the file row and links it to the user's library.
// It returns the item, whether it already existed, and any new tag names.
func (r *Repo) CreateItem(ctx context.Context, in CreateItemInput) (item *Item, existed bool, err error) {
	now := time.Now().Unix()
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()

	if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO files
		(sha256, ext, mime, size, width, height, animated, frames, created_at)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		in.File.Sha256, in.File.Ext, in.File.MIME, in.File.Size, in.File.Width,
		in.File.Height, boolInt(in.File.Animated), in.File.Frames, now); err != nil {
		return nil, false, err
	}

	var existingID int64
	row := tx.QueryRowContext(ctx, `SELECT id FROM items WHERE user_id=1 AND sha256=? AND deleted_at=0`, in.File.Sha256)
	switch err = row.Scan(&existingID); {
	case err == nil:
		return nil, true, tx.Commit()
	case !errors.Is(err, sql.ErrNoRows):
		return nil, false, err
	}

	res, err := tx.ExecContext(ctx, `INSERT INTO items
		(user_id, sha256, name, source_platform, source_hint, note, created_at)
		VALUES (1,?,?,?,?,?,?)`,
		in.File.Sha256, in.Name, in.SourcePlatform, in.SourceHint, in.Note, now)
	if err != nil {
		return nil, false, err
	}
	id, _ := res.LastInsertId()

	// Manual tags get source 'manual' so the AI job never overwrites them.
	for _, t := range in.Tags {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		tagID, err := upsertTag(ctx, tx, t, now)
		if err != nil {
			return nil, false, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO item_tags(item_id, tag_id, source) VALUES(?,?,'manual')`, id, tagID); err != nil {
			return nil, false, err
		}
	}
	for _, c := range in.Collections {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO item_collections(item_id, collection_id) VALUES(?,?)`, id, c); err != nil {
			return nil, false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO jobs(item_id, kind, status, created_at, updated_at)
		VALUES(?, 'tag', 'pending', ?, ?)`, id, now, now); err != nil {
		return nil, false, err
	}
	if err = tx.Commit(); err != nil {
		return nil, false, err
	}
	it, err := r.GetItem(ctx, id)
	return it, false, err
}

func upsertTag(ctx context.Context, tx *sql.Tx, name string, now int64) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM tags WHERE name=?`, name).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO tags(name, created_at) VALUES(?,?)`, name, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetItem loads one item by id (including soft-deleted ones).
func (r *Repo) GetItem(ctx context.Context, id int64) (*Item, error) {
	q := `SELECT i.id, i.sha256, f.ext, f.mime, f.size, f.width, f.height, f.animated, f.frames,
		i.name, i.source_platform, i.source_hint, i.note, i.favorite, i.use_count, i.last_used_at, i.created_at,
		COALESCE(a.description,''), COALESCE(a.ocr,'')
		FROM items i JOIN files f ON f.sha256 = i.sha256
		LEFT JOIN item_ai a ON a.item_id = i.id
		WHERE i.id = ?`
	row := r.DB.QueryRowContext(ctx, q, id)
	it := &Item{}
	var anim int
	if err := row.Scan(&it.ID, &it.Sha256, &it.Ext, &it.MIME, &it.Size, &it.Width, &it.Height,
		&anim, &it.Frames, &it.Name, &it.SourcePlatform, &it.SourceHint, &it.Note, &it.Favorite,
		&it.UseCount, &it.LastUsedAt, &it.CreatedAt, &it.AIDescription, &it.AIOCR); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	it.Animated = anim != 0
	if err := r.attachTags(ctx, []*Item{it}); err != nil {
		return nil, err
	}
	if err := r.attachCollections(ctx, []*Item{it}); err != nil {
		return nil, err
	}
	return it, nil
}

func (r *Repo) attachTags(ctx context.Context, items []*Item) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]string, 0, len(items))
	byID := map[int64]*Item{}
	for _, it := range items {
		ids = append(ids, strconv.FormatInt(it.ID, 10))
		byID[it.ID] = it
		it.Tags = []TagRef{}
	}
	q := `SELECT it.item_id, t.name, it.source FROM item_tags it JOIN tags t ON t.id=it.tag_id
		WHERE it.item_id IN (` + strings.Join(ids, ",") + `) ORDER BY t.name`
	rows, err := r.DB.QueryContext(ctx, q)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var itemID int64
		var ref TagRef
		if err := rows.Scan(&itemID, &ref.Name, &ref.Source); err != nil {
			return err
		}
		if it := byID[itemID]; it != nil {
			it.Tags = append(it.Tags, ref)
		}
	}
	return rows.Err()
}

func (r *Repo) attachCollections(ctx context.Context, items []*Item) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]string, 0, len(items))
	byID := map[int64]*Item{}
	for _, it := range items {
		ids = append(ids, strconv.FormatInt(it.ID, 10))
		byID[it.ID] = it
		it.Collections = []int64{}
	}
	q := `SELECT item_id, collection_id FROM item_collections WHERE item_id IN (` + strings.Join(ids, ",") + `)`
	rows, err := r.DB.QueryContext(ctx, q)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var itemID, cid int64
		if err := rows.Scan(&itemID, &cid); err != nil {
			return err
		}
		if it := byID[itemID]; it != nil {
			it.Collections = append(it.Collections, cid)
		}
	}
	return rows.Err()
}

// Filter describes a library query.
type Filter struct {
	Q            string
	Tags         []string
	Collection   int64 // >0 collection id, -4 = uncategorized, 0 = all
	FavoriteOnly bool
	Platform     string
	AnimatedOnly bool
	Sort         string // newest | oldest | name | used
	Limit        int
	Offset       int
	Deleted      bool // list the trash instead
}

func (f Filter) where() (string, []any) {
	var conds []string
	var args []any
	if f.Deleted {
		conds = append(conds, "i.deleted_at > 0")
	} else {
		conds = append(conds, "i.deleted_at = 0")
	}
	if q := strings.TrimSpace(f.Q); q != "" {
		like := "%" + escapeLike(q) + "%"
		conds = append(conds, `(i.name LIKE ? ESCAPE '\' OR i.note LIKE ? ESCAPE '\' OR i.sha256 LIKE ? ESCAPE '\'
			OR EXISTS(SELECT 1 FROM item_ai a WHERE a.item_id=i.id AND (a.description LIKE ? ESCAPE '\' OR a.ocr LIKE ? ESCAPE '\'))
			OR EXISTS(SELECT 1 FROM item_tags it JOIN tags t ON t.id=it.tag_id WHERE it.item_id=i.id AND t.name LIKE ? ESCAPE '\'))`)
		args = append(args, like, like, like, like, like, like)
	}
	for _, t := range f.Tags {
		if strings.TrimSpace(t) == "" {
			continue
		}
		conds = append(conds, `EXISTS(SELECT 1 FROM item_tags it JOIN tags t ON t.id=it.tag_id WHERE it.item_id=i.id AND t.name = ?)`)
		args = append(args, t)
	}
	switch f.Collection {
	case 0:
	case -4:
		conds = append(conds, `NOT EXISTS(SELECT 1 FROM item_collections ic WHERE ic.item_id=i.id)`)
	default:
		conds = append(conds, `EXISTS(SELECT 1 FROM item_collections ic WHERE ic.item_id=i.id AND ic.collection_id = ?)`)
		args = append(args, f.Collection)
	}
	if f.FavoriteOnly {
		conds = append(conds, "i.favorite = 1")
	}
	if f.Platform != "" {
		conds = append(conds, "i.source_platform = ?")
		args = append(args, f.Platform)
	}
	if f.AnimatedOnly {
		conds = append(conds, "f.animated = 1")
	}
	return strings.Join(conds, " AND "), args
}

func (f Filter) order() string {
	switch f.Sort {
	case "oldest":
		return "i.created_at ASC, i.id ASC"
	case "name":
		return "i.name COLLATE NOCASE ASC, i.id ASC"
	case "used":
		return "i.use_count DESC, i.last_used_at DESC, i.id DESC"
	default:
		return "i.created_at DESC, i.id DESC"
	}
}

// ListItems returns a page of items plus the total number matching the filter.
func (r *Repo) ListItems(ctx context.Context, f Filter) ([]*Item, int, error) {
	if f.Limit <= 0 {
		f.Limit = 60
	}
	if f.Limit > 300 {
		f.Limit = 300
	}
	where, args := f.where()

	var total int
	if err := r.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM items i JOIN files f ON f.sha256=i.sha256 WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	q := `SELECT i.id, i.sha256, f.ext, f.mime, f.size, f.width, f.height, f.animated, f.frames,
		i.name, i.source_platform, i.source_hint, i.note, i.favorite, i.use_count, i.last_used_at, i.created_at,
		COALESCE(a.description,''), COALESCE(a.ocr,'')
		FROM items i JOIN files f ON f.sha256=i.sha256
		LEFT JOIN item_ai a ON a.item_id=i.id
		WHERE ` + where + ` ORDER BY ` + f.order() + ` LIMIT ? OFFSET ?`
	args = append(args, f.Limit, f.Offset)
	rows, err := r.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []*Item{}
	for rows.Next() {
		it := &Item{}
		var anim int
		if err := rows.Scan(&it.ID, &it.Sha256, &it.Ext, &it.MIME, &it.Size, &it.Width, &it.Height,
			&anim, &it.Frames, &it.Name, &it.SourcePlatform, &it.SourceHint, &it.Note, &it.Favorite,
			&it.UseCount, &it.LastUsedAt, &it.CreatedAt, &it.AIDescription, &it.AIOCR); err != nil {
			return nil, 0, err
		}
		it.Animated = anim != 0
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if err := r.attachTags(ctx, out); err != nil {
		return nil, 0, err
	}
	if err := r.attachCollections(ctx, out); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// UpdateItem applies a partial update.
type UpdateItemInput struct {
	Name           *string
	Note           *string
	SourcePlatform *string
	SourceHint     *string
	Favorite       *bool
}

func (r *Repo) UpdateItem(ctx context.Context, id int64, in UpdateItemInput) error {
	sets := []string{}
	args := []any{}
	if in.Name != nil {
		sets = append(sets, "name = ?")
		args = append(args, *in.Name)
	}
	if in.Note != nil {
		sets = append(sets, "note = ?")
		args = append(args, *in.Note)
	}
	if in.SourcePlatform != nil {
		sets = append(sets, "source_platform = ?")
		args = append(args, *in.SourcePlatform)
	}
	if in.SourceHint != nil {
		sets = append(sets, "source_hint = ?")
		args = append(args, *in.SourceHint)
	}
	if in.Favorite != nil {
		sets = append(sets, "favorite = ?")
		args = append(args, boolInt(*in.Favorite))
	}
	if len(sets) == 0 {
		return nil
	}
	args = append(args, id)
	res, err := r.DB.ExecContext(ctx, `UPDATE items SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// UseItem records that the user copied the sticker.
func (r *Repo) UseItem(ctx context.Context, id int64) error {
	_, err := r.DB.ExecContext(ctx, `UPDATE items SET use_count = use_count + 1, last_used_at = ? WHERE id = ?`,
		time.Now().Unix(), id)
	return err
}

// TrashItem soft-deletes an item.
func (r *Repo) TrashItem(ctx context.Context, id int64) error {
	res, err := r.DB.ExecContext(ctx, `UPDATE items SET deleted_at = ? WHERE id = ? AND deleted_at = 0`,
		time.Now().Unix(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// RestoreItem un-deletes an item.
func (r *Repo) RestoreItem(ctx context.Context, id int64) error {
	_, err := r.DB.ExecContext(ctx, `UPDATE items SET deleted_at = 0 WHERE id = ?`, id)
	return err
}

// HardDelete removes the item permanently and returns the blob key of the
// (possibly now unused) file, plus whether the file row was dropped too.
func (r *Repo) HardDelete(ctx context.Context, id int64) (sha string, dropFile bool, err error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()
	if err := tx.QueryRowContext(ctx, `SELECT sha256 FROM items WHERE id=?`, id).Scan(&sha); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, ErrNotFound
		}
		return "", false, err
	}
	for _, q := range []string{
		`DELETE FROM item_tags WHERE item_id=?`,
		`DELETE FROM item_collections WHERE item_id=?`,
		`DELETE FROM item_ai WHERE item_id=?`,
		`DELETE FROM jobs WHERE item_id=?`,
		`DELETE FROM items WHERE id=?`,
	} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return "", false, err
		}
	}
	var refs int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM items WHERE sha256=?`, sha).Scan(&refs); err != nil {
		return "", false, err
	}
	if refs == 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM files WHERE sha256=?`, sha); err != nil {
			return "", false, err
		}
		dropFile = true
	}
	if err := tx.Commit(); err != nil {
		return "", false, err
	}
	_ = r.pruneOrphanTags(ctx)
	return sha, dropFile, nil
}

// EmptyTrash permanently deletes every trashed item.
func (r *Repo) EmptyTrash(ctx context.Context) ([]string, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT id FROM items WHERE deleted_at > 0`)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	var blobs []string
	for _, id := range ids {
		sha, drop, err := r.HardDelete(ctx, id)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return blobs, err
		}
		if drop {
			blobs = append(blobs, sha)
		}
	}
	return blobs, nil
}

// --- tags ---

// TagWithCount is a tag and how many live items carry it.
type TagWithCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// AddTag attaches a tag to an item (creating the tag if needed).
func (r *Repo) AddTag(ctx context.Context, itemID int64, name, source string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("empty tag")
	}
	now := time.Now().Unix()
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	id, err := upsertTag(ctx, tx, name, now)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO item_tags(item_id, tag_id, source) VALUES(?,?,?)`,
		itemID, id, source); err != nil {
		return err
	}
	return tx.Commit()
}

// RemoveTag detaches a tag from an item.
func (r *Repo) RemoveTag(ctx context.Context, itemID int64, name string) error {
	if _, err := r.DB.ExecContext(ctx,
		`DELETE FROM item_tags WHERE item_id=? AND tag_id=(SELECT id FROM tags WHERE name=?)`, itemID, name); err != nil {
		return err
	}
	return r.pruneOrphanTags(ctx)
}

func (r *Repo) pruneOrphanTags(ctx context.Context) error {
	_, err := r.DB.ExecContext(ctx,
		`DELETE FROM tags WHERE id NOT IN (SELECT DISTINCT tag_id FROM item_tags)`)
	return err
}

// Tags lists tags with live item counts.
func (r *Repo) Tags(ctx context.Context) ([]TagWithCount, error) {
	rows, err := r.DB.QueryContext(ctx, `
		SELECT t.name, COUNT(*) FROM tags t
		JOIN item_tags it ON it.tag_id = t.id
		JOIN items i ON i.id = it.item_id AND i.deleted_at = 0
		GROUP BY t.id ORDER BY COUNT(*) DESC, t.name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TagWithCount{}
	for rows.Next() {
		var t TagWithCount
		if err := rows.Scan(&t.Name, &t.Count); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// --- collections ---

// Collection is a folder of stickers (one level of nesting).
type Collection struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	ParentID *int64 `json:"parent_id"`
	Sort     int    `json:"sort"`
	Count    int    `json:"count"`
}

func (r *Repo) ListCollections(ctx context.Context) ([]Collection, error) {
	rows, err := r.DB.QueryContext(ctx, `
		SELECT c.id, c.name, c.parent_id, c.sort,
		  (SELECT COUNT(*) FROM item_collections ic JOIN items i ON i.id=ic.item_id
		     WHERE ic.collection_id=c.id AND i.deleted_at=0) AS cnt
		FROM collections c WHERE c.user_id=1 ORDER BY c.sort, c.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Collection{}
	for rows.Next() {
		var c Collection
		var parent sql.NullInt64
		if err := rows.Scan(&c.ID, &c.Name, &parent, &c.Sort, &c.Count); err != nil {
			return nil, err
		}
		if parent.Valid {
			p := parent.Int64
			c.ParentID = &p
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *Repo) CreateCollection(ctx context.Context, name string, parentID *int64) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, errors.New("empty collection name")
	}
	res, err := r.DB.ExecContext(ctx, `INSERT INTO collections(user_id, name, parent_id, sort, created_at)
		VALUES(1, ?, ?, 100, ?)`, name, parentID, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (r *Repo) RenameCollection(ctx context.Context, id int64, name string) error {
	_, err := r.DB.ExecContext(ctx, `UPDATE collections SET name=? WHERE id=?`, strings.TrimSpace(name), id)
	return err
}

func (r *Repo) DeleteCollection(ctx context.Context, id int64) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM item_collections WHERE collection_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE collections SET parent_id=NULL WHERE parent_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM collections WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// SetItemCollections replaces an item's collection memberships.
func (r *Repo) SetItemCollections(ctx context.Context, itemID int64, ids []int64) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM item_collections WHERE item_id=?`, itemID); err != nil {
		return err
	}
	for _, cid := range ids {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO item_collections(item_id, collection_id) VALUES(?,?)`,
			itemID, cid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// AddItemsToCollection adds many items to one collection.
func (r *Repo) AddItemsToCollection(ctx context.Context, ids []int64, collectionID int64) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO item_collections(item_id, collection_id) VALUES(?,?)`,
			id, collectionID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// --- AI metadata ---

// SetAI stores (replacing) the AI analysis of an item.
func (r *Repo) SetAI(ctx context.Context, itemID int64, description, ocr, model string) error {
	_, err := r.DB.ExecContext(ctx, `INSERT INTO item_ai(item_id, description, ocr, model, created_at)
		VALUES(?,?,?,?,?)
		ON CONFLICT(item_id) DO UPDATE SET description=excluded.description, ocr=excluded.ocr,
		  model=excluded.model, created_at=excluded.created_at`,
		itemID, description, ocr, model, time.Now().Unix())
	return err
}

// --- jobs ---

// Job is a queued background task.
type Job struct {
	ID       int64
	ItemID   int64
	Kind     string
	Status   string
	Attempts int
	Error    string
}

// Enqueue adds a tag job for an item.
func (r *Repo) Enqueue(ctx context.Context, itemID int64, kind string) error {
	now := time.Now().Unix()
	_, err := r.DB.ExecContext(ctx, `INSERT INTO jobs(item_id, kind, status, created_at, updated_at)
		VALUES(?,?, 'pending', ?, ?)`, itemID, kind, now, now)
	return err
}

// EnqueueAllUnanalyzed adds tag jobs for all active items that don't have an AI record or active job.
func (r *Repo) EnqueueAllUnanalyzed(ctx context.Context) (int64, error) {
	now := time.Now().Unix()
	res, err := r.DB.ExecContext(ctx, `
		INSERT INTO jobs(item_id, kind, status, created_at, updated_at)
		SELECT i.id, 'tag', 'pending', ?, ?
		FROM items i
		WHERE i.deleted_at = 0
		  AND NOT EXISTS (SELECT 1 FROM item_ai a WHERE a.item_id = i.id)
		  AND NOT EXISTS (SELECT 1 FROM jobs j WHERE j.item_id = i.id AND j.status IN ('pending', 'running'))
	`, now, now)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ResetFailedForRetry puts failed jobs back in the queue.
func (r *Repo) ResetFailedForRetry(ctx context.Context) (int64, error) {
	res, err := r.DB.ExecContext(ctx,
		`UPDATE jobs SET status='pending', error='', updated_at=? WHERE status='failed'`, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// ClaimJob marks the oldest pending job as running.
func (r *Repo) ClaimJob(ctx context.Context) (*Job, error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	j := &Job{}
	err = tx.QueryRowContext(ctx, `SELECT id, item_id, kind, status, attempts, error FROM jobs
		WHERE status='pending' ORDER BY id LIMIT 1`).Scan(&j.ID, &j.ItemID, &j.Kind, &j.Status, &j.Attempts, &j.Error)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE jobs SET status='running', attempts=attempts+1, updated_at=? WHERE id=?`,
		time.Now().Unix(), j.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	j.Status = "running"
	j.Attempts++
	return j, nil
}

// FinishJob records the outcome of a job.
func (r *Repo) FinishJob(ctx context.Context, jobID int64, jobErr error) error {
	status, msg := "done", ""
	if jobErr != nil {
		status, msg = "failed", jobErr.Error()
	}
	_, err := r.DB.ExecContext(ctx, `UPDATE jobs SET status=?, error=?, updated_at=? WHERE id=?`,
		status, msg, time.Now().Unix(), jobID)
	return err
}

// RequeueJob sends a job back to pending (used when the tagger is unavailable).
func (r *Repo) RequeueJob(ctx context.Context, jobID int64, reason string) error {
	_, err := r.DB.ExecContext(ctx, `UPDATE jobs SET status='pending', error=?, updated_at=? WHERE id=?`,
		reason, time.Now().Unix(), jobID)
	return err
}

// JobStats counts jobs by status.
func (r *Repo) JobStats(ctx context.Context) (map[string]int, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT status, COUNT(*) FROM jobs GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var s string
		var n int
		if err := rows.Scan(&s, &n); err != nil {
			return nil, err
		}
		out[s] = n
	}
	return out, rows.Err()
}

// --- stats ---

// Stats summarises the library.
func (r *Repo) Stats(ctx context.Context) (map[string]int64, error) {
	out := map[string]int64{}
	queries := map[string]string{
		"items":       `SELECT COUNT(*) FROM items WHERE deleted_at=0`,
		"trash":       `SELECT COUNT(*) FROM items WHERE deleted_at>0`,
		"animated":    `SELECT COUNT(*) FROM items i JOIN files f ON f.sha256=i.sha256 WHERE i.deleted_at=0 AND f.animated=1`,
		"favorites":   `SELECT COUNT(*) FROM items WHERE deleted_at=0 AND favorite=1`,
		"tags":        `SELECT COUNT(*) FROM tags`,
		"collections": `SELECT COUNT(*) FROM collections`,
		"bytes":       `SELECT COALESCE(SUM(f.size),0) FROM items i JOIN files f ON f.sha256=i.sha256 WHERE i.deleted_at=0`,
	}
	for k, q := range queries {
		var n int64
		if err := r.DB.QueryRowContext(ctx, q).Scan(&n); err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}
		out[k] = n
	}
	return out, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	return strings.ReplaceAll(s, `_`, `\_`)
}

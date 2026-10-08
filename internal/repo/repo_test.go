package repo

import (
	"context"
	"path/filepath"
	"testing"

	"mojinest/internal/db"
)

func testRepo(t *testing.T) *Repo {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return New(d)
}

func mkFile(sha string) NewFile {
	return NewFile{Sha256: sha, Ext: "png", MIME: "image/png", Size: 123, Width: 10, Height: 10, Frames: 1}
}

func TestCreateItemDedupesAndQueuesTagJob(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()

	it, existed, err := r.CreateItem(ctx, CreateItemInput{File: mkFile("aa11"), Name: "第一张", Tags: []string{"猫"}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if existed {
		t.Fatal("first insert must not report existed")
	}
	if it.Name != "第一张" || len(it.Tags) != 1 || it.Tags[0].Name != "猫" || it.Tags[0].Source != "manual" {
		t.Errorf("item = %+v", it)
	}

	if _, existed, err := r.CreateItem(ctx, CreateItemInput{File: mkFile("aa11")}); err != nil || !existed {
		t.Fatalf("duplicate should report existed=true, err=%v", err)
	}

	stats, err := r.JobStats(ctx)
	if err != nil {
		t.Fatalf("job stats: %v", err)
	}
	if stats["pending"] != 1 {
		t.Errorf("pending jobs = %d, want 1 (only the first insert queues)", stats["pending"])
	}
}

func TestFilterSearchTagsFavoritesTrash(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()

	a, _, _ := r.CreateItem(ctx, CreateItemInput{File: mkFile("a1"), Name: "猫猫大笑", Tags: []string{"猫", "笑"}})
	b, _, _ := r.CreateItem(ctx, CreateItemInput{File: mkFile("b2"), Name: "悲伤青蛙", Tags: []string{"青蛙"}})
	c, _, _ := r.CreateItem(ctx, CreateItemInput{File: mkFile("c3"), Name: "工作摸鱼", Tags: []string{"猫"}})

	if err := r.SetAI(ctx, b.ID, "一只流泪的青蛙", "我太难了", "test-model"); err != nil {
		t.Fatalf("set ai: %v", err)
	}

	cases := []struct {
		name string
		f    Filter
		want int
	}{
		{"all", Filter{}, 3},
		{"by name", Filter{Q: "青蛙"}, 1},
		{"by ai description", Filter{Q: "流泪"}, 1},
		{"by ocr", Filter{Q: "太难了"}, 1},
		{"by tag name substring", Filter{Q: "猫"}, 2},
		{"tag AND", Filter{Tags: []string{"猫", "笑"}}, 1},
		{"tag single", Filter{Tags: []string{"猫"}}, 2},
		{"uncategorized", Filter{Collection: -4}, 3},
		{"no match", Filter{Q: "不存在的东西"}, 0},
		{"trash empty", Filter{Deleted: true}, 0},
	}
	for _, tc := range cases {
		items, total, err := r.ListItems(ctx, tc.f)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if total != tc.want || len(items) != tc.want {
			t.Errorf("%s: total=%d items=%d want %d", tc.name, total, len(items), tc.want)
		}
	}

	// favourite + trash + filters
	fav := true
	if err := r.UpdateItem(ctx, a.ID, UpdateItemInput{Favorite: &fav}); err != nil {
		t.Fatalf("favorite: %v", err)
	}
	if _, total, _ := r.ListItems(ctx, Filter{FavoriteOnly: true}); total != 1 {
		t.Errorf("favorites = %d, want 1", total)
	}
	if err := r.UseItem(ctx, a.ID); err != nil {
		t.Fatalf("use: %v", err)
	}
	items, _, _ := r.ListItems(ctx, Filter{Sort: "used"})
	if items[0].ID != a.ID || items[0].UseCount != 1 || items[0].LastUsedAt == 0 {
		t.Errorf("most-used ordering broken: %+v", items[0])
	}
	if err := r.TrashItem(ctx, c.ID); err != nil {
		t.Fatalf("trash: %v", err)
	}
	if _, total, _ := r.ListItems(ctx, Filter{}); total != 2 {
		t.Errorf("live items = %d, want 2", total)
	}
	if _, total, _ := r.ListItems(ctx, Filter{Deleted: true}); total != 1 {
		t.Errorf("trash items = %d, want 1", total)
	}
	if err := r.RestoreItem(ctx, c.ID); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if _, total, _ := r.ListItems(ctx, Filter{}); total != 3 {
		t.Errorf("after restore = %d, want 3", total)
	}

	// keyword search must not be fooled by LIKE wildcards
	if _, _, err := r.CreateItem(ctx, CreateItemInput{File: mkFile("d4"), Name: "100%纯棉_款"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, total, _ := r.ListItems(ctx, Filter{Q: "%"}); total != 1 {
		t.Errorf("literal %% search matched %d rows, want 1", total)
	}
}

func TestTagsAndCollections(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()

	it, _, _ := r.CreateItem(ctx, CreateItemInput{File: mkFile("f1"), Tags: []string{"初始"}})

	if err := r.AddTag(ctx, it.ID, "AI打的", "ai"); err != nil {
		t.Fatalf("add tag: %v", err)
	}
	got, err := r.GetItem(ctx, it.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Tags) != 2 {
		t.Errorf("tags = %+v", got.Tags)
	}
	if err := r.RemoveTag(ctx, it.ID, "初始"); err != nil {
		t.Fatalf("remove tag: %v", err)
	}
	got, _ = r.GetItem(ctx, it.ID)
	if len(got.Tags) != 1 || got.Tags[0].Name != "AI打的" || got.Tags[0].Source != "ai" {
		t.Errorf("after remove = %+v", got.Tags)
	}
	tags, err := r.Tags(ctx)
	if err != nil {
		t.Fatalf("tags: %v", err)
	}
	if len(tags) != 1 || tags[0].Name != "AI打的" || tags[0].Count != 1 {
		t.Errorf("tag list = %+v (orphans must be pruned)", tags)
	}

	parent, err := r.CreateCollection(ctx, "工作", nil)
	if err != nil {
		t.Fatalf("create collection: %v", err)
	}
	child, err := r.CreateCollection(ctx, "同事", &parent)
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	if err := r.SetItemCollections(ctx, it.ID, []int64{child}); err != nil {
		t.Fatalf("set collections: %v", err)
	}
	if _, total, _ := r.ListItems(ctx, Filter{Collection: child}); total != 1 {
		t.Errorf("collection filter should find 1")
	}
	if _, total, _ := r.ListItems(ctx, Filter{Collection: -4}); total != 0 {
		t.Errorf("uncategorized should now be 0")
	}
	cols, err := r.ListCollections(ctx)
	if err != nil {
		t.Fatalf("list collections: %v", err)
	}
	if len(cols) != 3 { // 默认收藏 + 工作 + 同事
		t.Errorf("collections = %+v", cols)
	}
	for _, c := range cols {
		if c.Name == "同事" && (c.ParentID == nil || *c.ParentID != parent || c.Count != 1) {
			t.Errorf("child collection = %+v", c)
		}
	}
	if err := r.DeleteCollection(ctx, parent); err != nil {
		t.Fatalf("delete parent: %v", err)
	}
	cols, _ = r.ListCollections(ctx)
	for _, c := range cols {
		if c.Name == "同事" && c.ParentID != nil {
			t.Error("child should have been re-parented to root on delete")
		}
	}
}

func TestHardDeleteReleasesBlobOnlyWhenUnreferenced(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()

	first, _, _ := r.CreateItem(ctx, CreateItemInput{File: mkFile("zz")})
	if err := r.TrashItem(ctx, first.ID); err != nil {
		t.Fatalf("trash: %v", err)
	}

	sha, drop, err := r.HardDelete(ctx, first.ID)
	if err != nil {
		t.Fatalf("hard delete: %v", err)
	}
	if sha != "zz" || !drop {
		t.Errorf("sha=%q drop=%v, want zz/true (file no longer referenced)", sha, drop)
	}
	if _, err := r.GetItem(ctx, first.ID); err == nil {
		t.Error("item should be gone")
	}

	// A second row referencing the same sha must keep the blob alive.
	keep, _, _ := r.CreateItem(ctx, CreateItemInput{File: mkFile("yy")})
	_, drop2, err := r.HardDelete(ctx, keep.ID)
	if err != nil {
		t.Fatalf("hard delete: %v", err)
	}
	if !drop2 {
		t.Error("expected blob drop for single-reference file")
	}
}

func TestJobsLifecycle(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()
	it, _, _ := r.CreateItem(ctx, CreateItemInput{File: mkFile("jj")})

	job, err := r.ClaimJob(ctx)
	if err != nil || job == nil {
		t.Fatalf("claim: %v %v", job, err)
	}
	if job.ItemID != it.ID || job.Status != "running" || job.Attempts != 1 {
		t.Errorf("job = %+v", job)
	}
	if err := r.RequeueJob(ctx, job.ID, "model busy"); err != nil {
		t.Fatalf("requeue: %v", err)
	}
	job2, _ := r.ClaimJob(ctx)
	if job2 == nil || job2.Attempts != 2 {
		t.Fatalf("second claim = %+v", job2)
	}
	if err := r.FinishJob(ctx, job2.ID, nil); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if err := r.Enqueue(ctx, it.ID, "tag"); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	j3, _ := r.ClaimJob(ctx)
	if err := r.FinishJob(ctx, j3.ID, errTest); err != nil {
		t.Fatalf("finish fail: %v", err)
	}
	n, err := r.ResetFailedForRetry(ctx)
	if err != nil || n != 1 {
		t.Fatalf("reset failed: n=%d err=%v", n, err)
	}
	stats, _ := r.JobStats(ctx)
	if stats["pending"] != 1 || stats["failed"] != 0 {
		t.Errorf("stats = %+v", stats)
	}
}

var errTest = &testErr{}

type testErr struct{}

func (e *testErr) Error() string { return "boom" }

func TestStatsCounts(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()
	if _, _, err := r.CreateItem(ctx, CreateItemInput{File: mkFile("s1"), Tags: []string{"x"}}); err != nil {
		t.Fatalf("create: %v", err)
	}
	st, err := r.Stats(ctx)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if st["items"] != 1 || st["tags"] != 1 || st["bytes"] != 123 {
		t.Errorf("stats = %+v", st)
	}
}

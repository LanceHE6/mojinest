package api

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"mojinest/internal/config"
	"mojinest/internal/db"
	"mojinest/internal/repo"
	"mojinest/internal/store"
)

func newTestServer(t *testing.T, token string) (*Server, *httptest.Server, *httptest.Server) {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		Listen:  ":0",
		DataDir: dir,
		Token:   token,
		Storage: "local",
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("dirs: %v", err)
	}
	blobs, err := store.NewLocal(cfg.DataDir)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	sqlDB, err := db.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := New(repo.New(sqlDB), blobs, cfg, log)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, ts, nil
}

func pngFile(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x % 255), G: 120, B: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func gifFile(t *testing.T, frames int) []byte {
	t.Helper()
	pal := color.Palette{color.NRGBA{}, color.NRGBA{R: 10, G: 200, B: 10, A: 255}}
	g := &gif.GIF{Config: image.Config{ColorModel: pal, Width: 12, Height: 12}}
	for i := 0; i < frames; i++ {
		fr := image.NewPaletted(image.Rect(0, 0, 12, 12), pal)
		fr.SetColorIndex(i%12, 0, 1)
		g.Image = append(g.Image, fr)
		g.Delay = append(g.Delay, 10)
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		t.Fatalf("encode gif: %v", err)
	}
	return buf.Bytes()
}

func upload(t *testing.T, ts *httptest.Server, cookie *http.Cookie, files map[string][]byte, fields map[string]string) (*http.Response, []byte) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for name, data := range files {
		fw, err := mw.CreateFormFile("file", name)
		if err != nil {
			t.Fatalf("form file: %v", err)
		}
		if _, err := fw.Write(data); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	mw.Close()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/items", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp, out
}

func login(t *testing.T, ts *httptest.Server, token string) *http.Cookie {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"token": token})
	resp, err := ts.Client().Post(ts.URL+"/api/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d", resp.StatusCode)
	}
	for _, c := range resp.Cookies() {
		if c.Name != "" && c.Value != "" {
			return c
		}
	}
	t.Fatal("no cookie set")
	return nil
}

func getJSON(t *testing.T, ts *httptest.Server, cookie *http.Cookie, path string, out any) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("get %s: %v", path, err)
	}
	defer resp.Body.Close()
	if out != nil && resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
	}
	return resp.StatusCode
}

func TestAuthGuard(t *testing.T) {
	_, ts, _ := newTestServer(t, "secret-token")

	if code := getJSON(t, ts, nil, "/api/items", nil); code != http.StatusUnauthorized {
		t.Errorf("no cookie: %d, want 401", code)
	}
	// wrong token
	body, _ := json.Marshal(map[string]string{"token": "wrong"})
	resp, _ := ts.Client().Post(ts.URL+"/api/auth/login", "application/json", bytes.NewReader(body))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong token login: %d, want 401", resp.StatusCode)
	}
	resp.Body.Close()

	cookie := login(t, ts, "secret-token")
	if code := getJSON(t, ts, cookie, "/api/items", nil); code != http.StatusOK {
		t.Errorf("with cookie: %d, want 200", code)
	}

	// bearer header + query token also work (desktop clients)
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/stats", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	resp2, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("bearer: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("bearer: %d", resp2.StatusCode)
	}
	if code := getJSON(t, ts, nil, "/api/stats?token=secret-token", nil); code != http.StatusOK {
		t.Errorf("query token: %d", code)
	}
	// healthz stays public
	if code := getJSON(t, ts, nil, "/healthz", nil); code != http.StatusOK {
		t.Errorf("healthz: %d", code)
	}
}

func TestUploadDedupeThumbAndDelete(t *testing.T) {
	_, ts, _ := newTestServer(t, "tok")
	cookie := login(t, ts, "tok")

	pngData := pngFile(t, 400, 200)
	gifData := gifFile(t, 3)
	resp, body := upload(t, ts, cookie, map[string][]byte{"cat.png": pngData, "dog.gif": gifData},
		map[string]string{"tags": "测试,猫", "platform": "qq"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload status %d: %s", resp.StatusCode, body)
	}
	var up struct {
		Items []struct {
			ID             int64  `json:"id"`
			Sha256         string `json:"sha256"`
			Ext            string `json:"ext"`
			Width          int    `json:"width"`
			Height         int    `json:"height"`
			Animated       bool   `json:"animated"`
			Frames         int    `json:"frames"`
			Name           string `json:"name"`
			SourcePlatform string `json:"source_platform"`
			Tags           []struct {
				Name   string `json:"name"`
				Source string `json:"source"`
			} `json:"tags"`
		} `json:"items"`
		Skipped int `json:"skipped"`
	}
	if err := json.Unmarshal(body, &up); err != nil {
		t.Fatalf("decode upload: %v (%s)", err, body)
	}
	if len(up.Items) != 2 || up.Skipped != 0 {
		t.Fatalf("uploaded %d items, skipped %d", len(up.Items), up.Skipped)
	}
	for _, it := range up.Items {
		if len(it.Tags) != 2 {
			t.Errorf("item %s tags = %+v", it.Name, it.Tags)
		}
		if it.SourcePlatform != "qq" {
			t.Errorf("platform = %q", it.SourcePlatform)
		}
		switch it.Ext {
		case "png":
			if it.Width != 400 || it.Height != 200 || it.Animated {
				t.Errorf("png meta wrong: %+v", it)
			}
		case "gif":
			if !it.Animated || it.Frames != 3 || it.Width != 12 {
				t.Errorf("gif meta wrong: %+v", it)
			}
		default:
			t.Errorf("unexpected ext %q", it.Ext)
		}
	}

	// duplicate upload → skipped, no new item
	_, body2 := upload(t, ts, cookie, map[string][]byte{"copy.png": pngData}, nil)
	var dup struct {
		Skipped int `json:"skipped"`
	}
	_ = json.Unmarshal(body2, &dup)
	if dup.Skipped != 1 {
		t.Errorf("duplicate skipped = %d, want 1", dup.Skipped)
	}

	// non-image rejected with a readable error
	_, body3 := upload(t, ts, cookie, map[string][]byte{"junk.txt": []byte("just text")}, nil)
	if !strings.Contains(string(body3), "不是支持的图片格式") {
		t.Errorf("junk upload response = %s", body3)
	}

	// thumbnail exists and is an image
	var list struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	if code := getJSON(t, ts, cookie, "/api/items", &list); code != 200 || list.Total != 2 {
		t.Fatalf("list total=%d code=%d", list.Total, code)
	}
	sha := up.Items[0].Sha256
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/thumbs/"+sha, nil)
	req.AddCookie(cookie)
	tr, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("thumb: %v", err)
	}
	thumbBytes, _ := io.ReadAll(tr.Body)
	tr.Body.Close()
	if tr.StatusCode != 200 || (tr.Header.Get("Content-Type") != "image/png" && tr.Header.Get("Content-Type") != "image/jpeg") {
		t.Errorf("thumb: %d %s", tr.StatusCode, tr.Header.Get("Content-Type"))
	}
	if _, _, err := image.Decode(bytes.NewReader(thumbBytes)); err != nil {
		t.Errorf("thumb not decodable: %v", err)
	}
	// thumbnail requires auth
	if code := getJSON(t, ts, nil, "/api/thumbs/"+sha, nil); code != http.StatusUnauthorized {
		t.Errorf("thumb without auth = %d", code)
	}

	// hard delete frees the blob
	delReq, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/items/"+itoa(up.Items[0].ID), nil)
	delReq.AddCookie(cookie)
	dr, _ := ts.Client().Do(delReq)
	dr.Body.Close()
	if code := getJSON(t, ts, cookie, "/api/thumbs/"+sha, nil); code != http.StatusNotFound {
		t.Errorf("thumb after delete = %d, want 404", code)
	}
}

func TestItemUpdateTagCollectionBatch(t *testing.T) {
	s, ts, _ := newTestServer(t, "tok")
	cookie := login(t, ts, "tok")

	if _, body := upload(t, ts, cookie, map[string][]byte{"a.png": pngFile(t, 50, 50)}, nil); len(body) == 0 {
		t.Fatal("upload failed")
	}
	var list struct {
		Items []struct {
			ID int64 `json:"id"`
		} `json:"items"`
	}
	getJSON(t, ts, cookie, "/api/items", &list)
	id := list.Items[0].ID

	// PATCH
	patch, _ := json.Marshal(map[string]any{"name": "新名字", "favorite": true, "note": "备注"})
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/items/"+itoa(id), bytes.NewReader(patch))
	req.AddCookie(cookie)
	req.Header.Set("Content-Type", "application/json")
	pr, _ := ts.Client().Do(req)
	var item map[string]any
	_ = json.NewDecoder(pr.Body).Decode(&item)
	pr.Body.Close()
	if item["name"] != "新名字" || item["favorite"] != true || item["note"] != "备注" {
		t.Errorf("patched item = %+v", item)
	}

	// tag add/remove
	addBody, _ := json.Marshal(map[string]string{"name": "手动"})
	req, _ = http.NewRequest(http.MethodPost, ts.URL+"/api/items/"+itoa(id)+"/tags", bytes.NewReader(addBody))
	req.AddCookie(cookie)
	req.Header.Set("Content-Type", "application/json")
	ar, _ := ts.Client().Do(req)
	ar.Body.Close()
	if code := getJSON(t, ts, cookie, "/api/items?tags=手动", &list); code != 200 || len(list.Items) != 1 {
		t.Errorf("tag filter failed: %d %d", code, len(list.Items))
	}

	// collection create + assign via batch
	cb, _ := json.Marshal(map[string]any{"name": "群聊"})
	creq, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/collections", bytes.NewReader(cb))
	creq.AddCookie(cookie)
	creq.Header.Set("Content-Type", "application/json")
	resp, err := ts.Client().Do(creq)
	if err != nil {
		t.Fatalf("create collection: %v", err)
	}
	rawCreate, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(rawCreate, &created); err != nil {
		t.Fatalf("decode create: %v (%s)", err, rawCreate)
	}
	if created.ID == 0 {
		t.Fatalf("create collection returned %s", rawCreate)
	}

	batch, _ := json.Marshal(map[string]any{"action": "collection", "ids": []int64{id}, "collection_id": created.ID})
	req, _ = http.NewRequest(http.MethodPost, ts.URL+"/api/items/batch", bytes.NewReader(batch))
	req.AddCookie(cookie)
	req.Header.Set("Content-Type", "application/json")
	br, _ := ts.Client().Do(req)
	br.Body.Close()
	var cols []struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	if code := getJSON(t, ts, cookie, "/api/collections", &cols); code != 200 {
		t.Fatal("collections failed")
	}
	found := false
	for _, c := range cols {
		if c.Name == "群聊" && c.Count == 1 {
			found = true
		}
	}
	if !found {
		t.Errorf("collection counts wrong: %+v", cols)
	}

	// trash + trash listing + empty
	req, _ = http.NewRequest(http.MethodPost, ts.URL+"/api/items/"+itoa(id)+"/trash", nil)
	req.AddCookie(cookie)
	tr, _ := ts.Client().Do(req)
	tr.Body.Close()
	if code := getJSON(t, ts, cookie, "/api/items?trash=1", &list); code != 200 || len(list.Items) != 1 {
		t.Errorf("trash listing = %d %d", code, len(list.Items))
	}
	var stats map[string]int64
	getJSON(t, ts, cookie, "/api/stats", &stats)
	if stats["trash"] != 1 || stats["items"] != 0 {
		t.Errorf("stats = %+v", stats)
	}

	// export shape
	var exp struct {
		Version int `json:"version"`
		Items   []struct {
			ID int64 `json:"id"`
		} `json:"items"`
	}
	if code := getJSON(t, ts, cookie, "/api/export", &exp); code != 200 || exp.Version != 1 {
		t.Errorf("export = %d %+v", code, exp)
	}

	// jobs endpoint reports tagger state
	var jobs map[string]any
	getJSON(t, ts, cookie, "/api/jobs", &jobs)
	if _, ok := jobs["pending"]; !ok {
		t.Errorf("jobs payload = %+v", jobs)
	}
	_ = s
}

func TestCSRFOriginBlocked(t *testing.T) {
	_, ts, _ := newTestServer(t, "tok")
	cookie := login(t, ts, "tok")

	body, _ := json.Marshal(map[string]string{"name": "x"})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/collections", bytes.NewReader(body))
	req.AddCookie(cookie)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("csrf: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-origin write = %d, want 403", resp.StatusCode)
	}
}

func TestOpenInstanceAllowsAnonymous(t *testing.T) {
	_, ts, _ := newTestServer(t, "") // no token configured
	if code := getJSON(t, ts, nil, "/api/items", nil); code != http.StatusOK {
		t.Errorf("open instance should allow: %d", code)
	}
}

func TestStaticFallbackServesIndex(t *testing.T) {
	_, ts, _ := newTestServer(t, "tok")
	resp, err := ts.Client().Get(ts.URL + "/some/spa/route")
	if err != nil {
		t.Fatalf("static: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !bytes.Contains(body, []byte("Mojinest")) {
		t.Errorf("SPA fallback: %d %.60s", resp.StatusCode, body)
	}
	if code := getJSON(t, ts, nil, "/api/nope", nil); code != http.StatusNotFound {
		t.Errorf("unknown api = %d, want 404", code)
	}
}

func itoa(i int64) string {
	return strconv.FormatInt(i, 10)
}

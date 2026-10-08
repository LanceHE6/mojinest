package tagger

import "testing"

func TestParseContentPlainJSON(t *testing.T) {
	res, err := parseContent(`{"description":"猫在笑","ocr":"哈哈","tags":["猫","笑"]}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if res.Description != "猫在笑" || res.OCR != "哈哈" || len(res.Tags) != 2 {
		t.Errorf("got %+v", res)
	}
}

func TestParseContentStripsFencesAndProse(t *testing.T) {
	raw := "好的，这是结果：\n```json\n{\"description\":\"无语\",\"ocr\":\"\",\"tags\":[\"#无语\",\" 猫 \",\"无语\"]}\n```\n希望有帮助"
	res, err := parseContent(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if res.Description != "无语" {
		t.Errorf("description = %q", res.Description)
	}
	if len(res.Tags) != 2 || res.Tags[0] != "无语" || res.Tags[1] != "猫" {
		t.Errorf("tags = %#v (want dedup + '#' and spaces stripped, order kept)", res.Tags)
	}
}

func TestParseContentRejectsGarbage(t *testing.T) {
	if _, err := parseContent("完全不是 JSON"); err == nil {
		t.Error("expected parse error")
	}
}

func TestCleanTags(t *testing.T) {
	got := cleanTags([]string{"猫", "#猫", "", "   ", "这个标签实在是太长了超过限制了", "笑"})
	if len(got) != 2 || got[0] != "猫" || got[1] != "笑" {
		t.Errorf("cleanTags = %#v", got)
	}
}

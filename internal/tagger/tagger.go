// Package tagger calls an OpenAI-compatible vision model to describe a
// sticker and propose Chinese tags.
package tagger

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const defaultPrompt = `你在给一个表情包（贴图/梗图）库做自动标注。请只输出一个 JSON 对象，不要有任何多余文字、不要用 markdown 代码块。
字段：
- "description": 一句话描述这张图的内容和情绪（中文，40 字以内）
- "ocr": 图中出现的文字，逐条用 / 分隔；没有文字就空字符串
- "tags": 6 到 12 个中文标签，覆盖这些维度：情绪/态度（如 笑、无语、生气、点赞）、人物或角色（如 猫、狗、熊猫头）、动作、使用场景（如 打招呼、道谢、吐槽）、风格（如 沙雕、可爱、黑白）
标签请用常见短词，不要带 # 号，不要重复。`

// Tagger analyses images with a vision model.
type Tagger struct {
	BaseURL string
	APIKey  string
	Model   string
	Prompt  string
	Timeout time.Duration
	Client  *http.Client
	MaxSide int
}

// Result is the model output for one image.
type Result struct {
	Description string   `json:"description"`
	OCR         string   `json:"ocr"`
	Tags        []string `json:"tags"`
	Model       string   `json:"-"`
}

// New builds a tagger pointed at an OpenAI-compatible endpoint.
func New(baseURL, apiKey, model, prompt string, timeout time.Duration) *Tagger {
	if strings.TrimSpace(prompt) == "" {
		prompt = defaultPrompt
	}
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	return &Tagger{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		Model:   model,
		Prompt:  prompt,
		Timeout: timeout,
		Client:  &http.Client{Timeout: timeout},
		MaxSide: 512,
	}
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
}

type chatMessage struct {
	Role    string        `json:"role"`
	Content []contentPart `json:"content"`
}

type contentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *imageURL `json:"image_url,omitempty"`
}

type imageURL struct {
	URL string `json:"url"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Analyze sends the (PNG-encoded) image to the model and parses its JSON reply.
func (t *Tagger) Analyze(ctx context.Context, pngBytes []byte) (*Result, error) {
	if t.BaseURL == "" {
		return nil, fmt.Errorf("tagger: base_url is not configured")
	}
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes)
	reqBody := chatRequest{
		Model: t.Model,
		Messages: []chatMessage{{
			Role: "user",
			Content: []contentPart{
				{Type: "text", Text: t.Prompt},
				{Type: "image_url", ImageURL: &imageURL{URL: dataURL}},
			},
		}},
		Temperature: 0.2,
	}
	raw, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, t.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.BaseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if t.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+t.APIKey)
	}
	resp, err := t.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("tagger: model HTTP %d: %s", resp.StatusCode, truncate(string(body), 300))
	}
	var parsed chatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("tagger: bad response: %w", err)
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return nil, fmt.Errorf("tagger: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return nil, fmt.Errorf("tagger: empty response")
	}
	res, err := parseContent(parsed.Choices[0].Message.Content)
	if err != nil {
		return nil, err
	}
	res.Model = t.Model
	return res, nil
}

var fenceRe = regexp.MustCompile("(?s)```(?:json)?\\s*(.*?)\\s*```")

func parseContent(s string) (*Result, error) {
	s = strings.TrimSpace(s)
	if m := fenceRe.FindStringSubmatch(s); len(m) == 2 {
		s = strings.TrimSpace(m[1])
	}
	if i := strings.Index(s, "{"); i >= 0 {
		if j := strings.LastIndex(s, "}"); j > i {
			s = s[i : j+1]
		}
	}
	var out Result
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil, fmt.Errorf("tagger: cannot parse model output: %w", err)
	}
	out.Description = strings.TrimSpace(out.Description)
	out.OCR = strings.TrimSpace(out.OCR)
	out.Tags = cleanTags(out.Tags)
	return &out, nil
}

func cleanTags(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, t := range in {
		t = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(t), "#"))
		t = strings.Trim(t, "，,。.、;；")
		if t == "" || len([]rune(t)) > 12 || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
		if len(out) >= 16 {
			break
		}
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

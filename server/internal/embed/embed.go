// Package embed 调用 OpenAI 兼容的 /embeddings 接口把文本转成向量。
//
// 缺省对接 docker-compose 里的 HF text-embeddings-inference（TEI）+ BAAI/bge-small-zh-v1.5，
// 也可以换成任意 OpenAI 兼容的云端嵌入服务。返回的向量统一归一化为单位长度，余弦相似度 = 点积。
package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"zhiwei/internal/settings"
)

// maxInputRunes 单条输入上限。TEI 需带 --auto-truncate 才会按模型上限截断，这里先粗截一道兜底。
const maxInputRunes = 2000

type Client struct {
	base, key, model string
	http             *http.Client
}

func New(cfg settings.Embedding) *Client {
	return &Client{
		base:  strings.TrimRight(cfg.BaseURL, "/"),
		key:   cfg.APIKey,
		model: cfg.Model,
		http:  &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) Model() string { return c.model }

// Embed 批量向量化，结果与 texts 一一对应。
func (c *Client) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	in := make([]string, len(texts))
	for i, t := range texts {
		if r := []rune(t); len(r) > maxInputRunes {
			t = string(r[:maxInputRunes])
		}
		if strings.TrimSpace(t) == "" {
			t = "-"
		}
		in[i] = t
	}
	body, _ := json.Marshal(map[string]any{"model": c.model, "input": in})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.key != "" {
		req.Header.Set("Authorization", "Bearer "+c.key)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("嵌入服务返回 HTTP %d：%s", resp.StatusCode, truncate(string(raw), 200))
	}
	var out struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("嵌入服务返回无法解析：%s", truncate(string(raw), 200))
	}
	if len(out.Data) != len(in) {
		return nil, fmt.Errorf("嵌入服务返回 %d 条向量，期望 %d 条", len(out.Data), len(in))
	}
	sort.Slice(out.Data, func(i, j int) bool { return out.Data[i].Index < out.Data[j].Index })
	vecs := make([][]float32, len(out.Data))
	for i, d := range out.Data {
		if len(d.Embedding) == 0 {
			return nil, fmt.Errorf("嵌入服务返回了空向量")
		}
		vecs[i] = Normalize(d.Embedding)
	}
	return vecs, nil
}

// Normalize 原地归一化为单位向量。
func Normalize(v []float32) []float32 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return v
	}
	n := float32(1 / math.Sqrt(sum))
	for i := range v {
		v[i] *= n
	}
	return v
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

package kb_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"zhiwei/db/migrations"
	"zhiwei/internal/kb"
	"zhiwei/internal/store"
)

// conceptEmbedder 按概念打向量：含「充」的文本落在充值轴，含「Key」的落在密钥轴，其余落在杂项轴。
// 用来模拟「怎么充钱」与「充值方法」字面不重合、语义相近的场景。
type conceptEmbedder struct{}

func (conceptEmbedder) Model() string { return "concept" }

func (conceptEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		switch {
		case strings.Contains(t, "充"):
			out[i] = []float32{1, 0, 0}
		case strings.Contains(t, "Key"):
			out[i] = []float32{0, 1, 0}
		default:
			out[i] = []float32{0, 0, 1}
		}
	}
	return out, nil
}

func TestHybridSearch(t *testing.T) {
	dsn := os.Getenv("ZHIWEI_TEST_DSN")
	if dsn == "" {
		t.Skip("未设置 ZHIWEI_TEST_DSN，跳过数据库测试")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if _, err := st.Pool().Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Migrate(ctx, st.Pool(), migrations.PG, migrations.PGDir); err != nil {
		t.Fatal(err)
	}
	for _, d := range []store.KBDocument{
		{Path: "计费", Title: "充值", Content: "充值方法：在控制台充值页选择金额，支持支付宝。"},
		{Path: "接入", Title: "密钥", Content: "API Key 在控制台令牌页面创建。"},
	} {
		d.Enabled = true
		if err := st.SaveKBDocument(ctx, &d, kb.Split(d.Title, d.Content)); err != nil {
			t.Fatal(err)
		}
	}

	ix := kb.NewIndex(st)
	if err := ix.Rebuild(ctx); err != nil {
		t.Fatal(err)
	}
	if hits := ix.Search(ctx, "怎么充钱", 3); len(hits) != 0 {
		t.Fatalf("纯关键词下「充钱」不应命中「充值」：%+v", hits)
	}

	ix.Configure(ctx, conceptEmbedder{}, 0.5)
	go ix.RunEmbedder(ctx)
	waitEmbedded(t, ix, 2)
	hits := ix.Search(ctx, "怎么充钱", 3)
	if len(hits) != 1 || hits[0].DocTitle != "充值" || hits[0].DocPath != "计费" || hits[0].Vector < 0.99 || hits[0].Keyword != 0 {
		t.Fatalf("向量一路应召回「充值」：%+v", hits)
	}
	// 两路都命中：Keyword、Vector 两个分都有
	hits = ix.Search(ctx, "控制台 API Key", 3)
	if len(hits) == 0 || hits[0].DocTitle != "密钥" || hits[0].Keyword == 0 || hits[0].Vector == 0 {
		t.Fatalf("融合排序不对：%+v", hits)
	}

	// 嵌入服务挂了：退回关键词，状态里带错误
	ix.Configure(ctx, failingEmbedder{}, 0.5)
	if hits := ix.Search(ctx, "控制台令牌", 3); len(hits) == 0 || hits[0].Vector != 0 {
		t.Fatalf("嵌入失败应退回关键词：%+v", hits)
	}
	if st := ix.Status(); st.LastError == "" || st.Embedded != 2 {
		t.Fatalf("状态应记录错误且保留已有向量：%+v", st)
	}
}

// failingEmbedder 与 conceptEmbedder 同模型名（已有向量仍有效），但调用总失败。
type failingEmbedder struct{}

func (failingEmbedder) Model() string { return "concept" }
func (failingEmbedder) Embed(context.Context, []string) ([][]float32, error) {
	return nil, errors.New("connection refused")
}

func waitEmbedded(t *testing.T, ix *kb.Index, n int) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if ix.Status().Embedded >= n {
			return
		}
		ix.Kick()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("向量化未完成：%+v", ix.Status())
}

package agent

import (
	"testing"
	"time"

	"zhiwei/internal/store"
)

func TestResolveTopics(t *testing.T) {
	srcs := make([]store.TopicSource, 6)
	for i := range srcs {
		srcs[i] = store.TopicSource{ID: int64(i), At: time.Now()}
	}
	id := func(v int64) *int64 { return &v }
	str := func(v string) *string { return &v }
	pool := []store.QuestionTopic{{ID: 1, Title: "退款到账时间"}, {ID: 2, Title: "API Key 创建方法"}}
	known := map[int64]bool{1: true} // 只有 1 号交给了模型
	out := resolveTopics([]topicItem{
		{I: 0, TopicID: id(1)},
		{I: 1, TopicID: id(2)},                // 不在交给模型的清单里（幻觉）→ 丢弃
		{I: 2, NewTitle: str("API Key 创建方法")}, // 与已有主题同名 → 并入 2
		{I: 3, NewTitle: str("充值方式")},         // 新主题
		{I: 4, NewTitle: str("充值方式")},         // 同批同名 → 共用
		{I: 4, Noise: true},                   // 重复序号 → 忽略
		{I: 5, Noise: true},
		{I: 9, NewTitle: str("越界")},
	}, srcs, known, pool)
	if len(out) != 4 {
		t.Fatalf("应有 4 条归类：%+v", out)
	}
	if out[0].TopicID != 1 || out[1].TopicID != 2 || out[2].TopicID != 0 || out[2].NewTitle != "充值方式" || out[3].NewTitle != "充值方式" {
		t.Fatalf("归类结果：%+v", out)
	}
}

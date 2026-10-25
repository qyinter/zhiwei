package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"zhiwei/internal/llm"
	"zhiwei/internal/store"
)

var (
	errNoBrain = errors.New("LLM 未配置")
	// errBadOutput 模型答了，但输出不是要求的 JSON
	errBadOutput = errors.New("模型输出不合格式")
)

// askJSON 后台任务的一次性结构化调用（无工具、无历史）：system 恒定、数据全在 user 侧，
// 要求模型只输出 JSON 对象。不同模型 / 协议对 JSON mode 支持不一，这里只靠提示词约束 + 宽松解析。
func (o *Orchestrator) askJSON(ctx context.Context, purpose, system, user string, out any) error {
	_, brain := o.snapshot()
	if brain == nil {
		return errNoBrain
	}
	start := time.Now()
	resp, err := brain.client.Chat(ctx, llm.Request{
		System:   system,
		Messages: []llm.Message{{Role: llm.RoleUser, Content: user}},
	})
	call := store.LLMCall{
		Model: brain.client.Model(), InputTokens: resp.Usage.InputTokens, OutputTokens: resp.Usage.OutputTokens,
		CacheReadTokens: resp.Usage.CacheReadTokens, CacheWriteTokens: resp.Usage.CacheWriteTokens,
		LatencyMs: int(time.Since(start).Milliseconds()), Purpose: purpose,
	}
	if resp.Model != "" {
		call.Model = resp.Model
	}
	if err == nil {
		err = parseJSONObject(resp.Content, out)
	}
	if err != nil {
		call.Error = truncateRunes(err.Error(), 1000)
	}
	recCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if rerr := o.st.RecordLLMCall(recCtx, call); rerr != nil {
		o.log.Warn("记录用量失败", "err", rerr)
	}
	return err
}

// parseJSONObject 从模型输出里取出第一个 { 到最后一个 } 之间的 JSON（容忍 ```json 围栏与前后废话）。
func parseJSONObject(s string, out any) error {
	i, j := strings.Index(s, "{"), strings.LastIndex(s, "}")
	if i < 0 || j < i {
		return fmt.Errorf("%w：未输出 JSON：%s", errBadOutput, truncateRunes(s, 200))
	}
	if err := json.Unmarshal([]byte(s[i:j+1]), out); err != nil {
		return fmt.Errorf("%w：解析 JSON 失败：%w", errBadOutput, err)
	}
	return nil
}

// jsonl 把若干对象编码成每行一个 JSON，放进 user 侧数据块。
func jsonl[T any](items []T) string {
	var sb strings.Builder
	for _, it := range items {
		raw, _ := json.Marshal(it)
		sb.Write(raw)
		sb.WriteByte('\n')
	}
	return sb.String()
}

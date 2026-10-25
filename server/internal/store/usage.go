package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// UsageTotals 一组 llm_calls 的累计。InputTokens 是全部输入（含缓存命中与缓存写入），
// 命中率 = CacheReadTokens / InputTokens。
type UsageTotals struct {
	Calls            int64 `json:"calls"`
	Errors           int64 `json:"errors"`
	InputTokens      int64 `json:"inputTokens"`
	OutputTokens     int64 `json:"outputTokens"`
	CacheReadTokens  int64 `json:"cacheReadTokens"`
	CacheWriteTokens int64 `json:"cacheWriteTokens"`
	Images           int64 `json:"images"`
	AvgLatencyMs     int64 `json:"avgLatencyMs"`
}

const usageAgg = `count(*), count(*) FILTER (WHERE error <> ''), coalesce(sum(input_tokens), 0), coalesce(sum(output_tokens), 0),
	coalesce(sum(cache_read_tokens), 0), coalesce(sum(cache_write_tokens), 0), coalesce(sum(images), 0),
	coalesce(avg(latency_ms), 0)::bigint`

func (t *UsageTotals) dest() []any {
	return []any{&t.Calls, &t.Errors, &t.InputTokens, &t.OutputTokens, &t.CacheReadTokens, &t.CacheWriteTokens, &t.Images, &t.AvgLatencyMs}
}

type UsageDay struct {
	// Day 按请求时区的日期 YYYY-MM-DD
	Day string `json:"day"`
	UsageTotals
}

type UsageGroup struct {
	Key string `json:"key"`
	UsageTotals
}

// ToolUsage Calls 调用次数；Runs 出现过该工具的推理轮数（每轮每工具最多算 1）。
type ToolUsage struct {
	Tool  string `json:"tool"`
	Calls int64  `json:"calls"`
	Runs  int64  `json:"runs"`
}

type UsageError struct {
	At        time.Time `json:"at"`
	Purpose   string    `json:"purpose"`
	Model     string    `json:"model"`
	Error     string    `json:"error"`
	LatencyMs int       `json:"latencyMs"`
}

type Usage struct {
	Totals    UsageTotals  `json:"totals"`
	Days      []UsageDay   `json:"days"`
	ByPurpose []UsageGroup `json:"byPurpose"`
	ByModel   []UsageGroup `json:"byModel"`
	Tools     []ToolUsage  `json:"tools"`
	// Errors 最近 20 次失败的调用
	Errors []UsageError `json:"errors"`
}

// UsageSince from 之后的用量；按天分组用 tz（IANA 时区名，调用方已校验）切日。
func (s *Store) UsageSince(ctx context.Context, from time.Time, tz string) (Usage, error) {
	u := Usage{Days: []UsageDay{}, ByPurpose: []UsageGroup{}, ByModel: []UsageGroup{}, Tools: []ToolUsage{}, Errors: []UsageError{}}
	if err := s.pool.QueryRow(ctx, `SELECT `+usageAgg+` FROM llm_calls WHERE created_at >= $1`, from).Scan(u.Totals.dest()...); err != nil {
		return u, err
	}

	rows, err := s.pool.Query(ctx, `SELECT to_char(created_at AT TIME ZONE $2, 'YYYY-MM-DD') AS day, `+usageAgg+`
		FROM llm_calls WHERE created_at >= $1 GROUP BY day ORDER BY day`, from, tz)
	if err != nil {
		return u, err
	}
	u.Days, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (UsageDay, error) {
		var d UsageDay
		err := r.Scan(append([]any{&d.Day}, d.dest()...)...)
		return d, err
	})
	if err != nil {
		return u, err
	}

	for _, g := range []struct {
		col string
		out *[]UsageGroup
	}{{"purpose", &u.ByPurpose}, {"model", &u.ByModel}} {
		rows, err := s.pool.Query(ctx, `SELECT `+g.col+`, `+usageAgg+` FROM llm_calls WHERE created_at >= $1
			GROUP BY 1 ORDER BY sum(input_tokens) + sum(output_tokens) DESC`, from)
		if err != nil {
			return u, err
		}
		*g.out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (UsageGroup, error) {
			var x UsageGroup
			err := r.Scan(append([]any{&x.Key}, x.dest()...)...)
			return x, err
		})
		if err != nil {
			return u, err
		}
	}

	rows, err = s.pool.Query(ctx, `SELECT t.tool, count(*), count(DISTINCT c.id)
		FROM llm_calls c CROSS JOIN LATERAL unnest(c.tool_names) AS t(tool)
		WHERE c.created_at >= $1 GROUP BY t.tool ORDER BY count(*) DESC`, from)
	if err != nil {
		return u, err
	}
	u.Tools, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (ToolUsage, error) {
		var t ToolUsage
		err := r.Scan(&t.Tool, &t.Calls, &t.Runs)
		return t, err
	})
	if err != nil {
		return u, err
	}

	rows, err = s.pool.Query(ctx, `SELECT created_at, purpose, model, error, latency_ms FROM llm_calls
		WHERE created_at >= $1 AND error <> '' ORDER BY created_at DESC LIMIT 20`, from)
	if err != nil {
		return u, err
	}
	u.Errors, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (UsageError, error) {
		var e UsageError
		err := r.Scan(&e.At, &e.Purpose, &e.Model, &e.Error, &e.LatencyMs)
		return e, err
	})
	return u, err
}

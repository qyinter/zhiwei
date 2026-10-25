// Package kb 知识库切块与检索。
//
// 检索全在进程内：关键词一路是 BM25（中文按字二元组、英文数字按词），向量一路是余弦相似度
// （向量存在 PG 的 REAL[] 列里，由外部嵌入服务生成）。客服知识库通常几十到几千块，全量常驻内存
// 毫无压力，且不依赖 pgvector / zhparser 等 PG 扩展——部署只需要一个原生 PostgreSQL。
package kb

import (
	"context"
	"log/slog"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"zhiwei/internal/store"
)

const (
	chunkMaxRunes = 500
	bm25K1        = 1.2
	bm25B         = 0.75
)

// Split 按 Markdown 标题与空行把文档切成 ≤500 字的块，每块带最近的标题路径。
// 超长段落按句号硬切；标题本身也计入块正文，便于「问标题」类查询命中。
func Split(title, content string) []store.ChunkInput {
	var (
		out     []store.ChunkInput
		heading string
		buf     strings.Builder
	)
	flush := func() {
		text := strings.TrimSpace(buf.String())
		buf.Reset()
		for text != "" {
			r := []rune(text)
			if len(r) <= chunkMaxRunes {
				out = append(out, store.ChunkInput{Heading: heading, Content: text})
				return
			}
			cut := chunkMaxRunes
			for i := chunkMaxRunes - 1; i > chunkMaxRunes/2; i-- {
				if strings.ContainsRune("。！？!?\n；;", r[i]) {
					cut = i + 1
					break
				}
			}
			out = append(out, store.ChunkInput{Heading: heading, Content: strings.TrimSpace(string(r[:cut]))})
			text = strings.TrimSpace(string(r[cut:]))
		}
	}
	for _, para := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n\n") {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		if strings.HasPrefix(para, "#") {
			flush()
			line, rest, _ := strings.Cut(para, "\n")
			heading = strings.TrimSpace(strings.TrimLeft(line, "#"))
			para = strings.TrimSpace(rest)
			if para == "" {
				continue
			}
		}
		if buf.Len() > 0 && len([]rune(buf.String()))+len([]rune(para)) > chunkMaxRunes {
			flush()
		}
		if buf.Len() > 0 {
			buf.WriteString("\n\n")
		}
		buf.WriteString(para)
	}
	flush()
	if len(out) == 0 && strings.TrimSpace(title) != "" {
		out = append(out, store.ChunkInput{Content: strings.TrimSpace(title)})
	}
	return out
}

// tokenize 中文连续段落出二元组（单字段落出单字），ASCII 字母数字按词小写。
func tokenize(s string) []string {
	var (
		toks []string
		word []rune
		han  []rune
	)
	flushWord := func() {
		if len(word) > 0 {
			toks = append(toks, strings.ToLower(string(word)))
			word = word[:0]
		}
	}
	flushHan := func() {
		if len(han) == 1 {
			toks = append(toks, string(han))
		}
		for i := 0; i+1 < len(han); i++ {
			toks = append(toks, string(han[i:i+2]))
		}
		han = han[:0]
	}
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Han, r):
			flushWord()
			han = append(han, r)
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.':
			flushHan()
			word = append(word, r)
		default:
			flushWord()
			flushHan()
		}
	}
	flushWord()
	flushHan()
	return toks
}

type doc struct {
	chunk store.KBChunk
	tf    map[string]int
	len   int
}

type Hit struct {
	ChunkID    int64   `json:"chunkId"`
	DocumentID int64   `json:"documentId"`
	DocPath    string  `json:"docPath,omitempty"`
	DocTitle   string  `json:"docTitle"`
	Heading    string  `json:"heading"`
	Content    string  `json:"content"`
	Score      float64 `json:"score"`
	// Keyword / Vector 两路各自的得分（BM25 / 余弦相似度），没有命中该路为 0
	Keyword float64 `json:"keyword"`
	Vector  float64 `json:"vector"`
	// ReflectionID 命中的是反思沉淀条目时非 0
	ReflectionID int64 `json:"reflectionId,omitempty"`
}

// Embedder 文本向量化（internal/embed 实现）。返回的向量须已归一化。
type Embedder interface {
	Model() string
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

const (
	// rrfK 倒数排名融合常数（业界惯用 60）：两路排名都靠前的块胜出，单路第一也不至于被埋没
	rrfK = 60
	// queryEmbedTimeout 检索时向量化问句的超时，超时即退回纯关键词
	queryEmbedTimeout = 3 * time.Second
	embedBatch        = 32
)

// EmbedStatus 后台知识库页展示的向量化进度。
type EmbedStatus struct {
	Enabled     bool       `json:"enabled"`
	Model       string     `json:"model"`
	Total       int        `json:"total"`
	Embedded    int        `json:"embedded"`
	LastError   string     `json:"lastError,omitempty"`
	LastErrorAt *time.Time `json:"lastErrorAt,omitempty"`
}

// Index 只读快照 + 原子替换：重建期间检索继续用旧快照。
// 关键词（BM25）一路始终可用；配置了 Embedder 且块已向量化时再加一路余弦相似度，两路按 RRF 融合。
type Index struct {
	st  *store.Store
	log *slog.Logger

	mu       sync.RWMutex
	docs     []doc
	df       map[string]int
	avgLen   float64
	vecCount int
	emb      Embedder
	minScore float64
	lastErr  string
	errAt    time.Time

	// rebuildMu 串行化重建，避免慢的旧快照覆盖新快照
	rebuildMu sync.Mutex
	kick      chan struct{}
}

func NewIndex(st *store.Store) *Index {
	return &Index{st: st, log: slog.Default().With("mod", "kb"), df: map[string]int{}, kick: make(chan struct{}, 1)}
}

// Configure 切换嵌入服务（nil = 关闭向量检索）。模型变了要重建快照（旧向量作废）并唤醒向量化任务。
func (ix *Index) Configure(ctx context.Context, e Embedder, minScore float64) {
	ix.mu.Lock()
	changed := model(ix.emb) != model(e)
	ix.emb, ix.minScore = e, minScore
	if changed {
		ix.lastErr, ix.errAt = "", time.Time{}
	}
	ix.mu.Unlock()
	if changed {
		if err := ix.Rebuild(ctx); err != nil {
			ix.log.Error("重建知识库索引失败", "err", err)
		}
	}
}

func model(e Embedder) string {
	if e == nil {
		return ""
	}
	return e.Model()
}

func (ix *Index) embedder() (Embedder, float64) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return ix.emb, ix.minScore
}

// Rebuild 重新加载全部块，并唤醒向量化任务处理新增 / 改动的块。
func (ix *Index) Rebuild(ctx context.Context) error {
	err := ix.rebuild(ctx)
	ix.Kick()
	return err
}

// Kick 唤醒向量化任务（不阻塞）。
func (ix *Index) Kick() {
	select {
	case ix.kick <- struct{}{}:
	default:
	}
}

func (ix *Index) rebuild(ctx context.Context) error {
	ix.rebuildMu.Lock()
	defer ix.rebuildMu.Unlock()
	emb, _ := ix.embedder()
	chunks, err := ix.st.EnabledKBChunks(ctx, model(emb))
	if err != nil {
		return err
	}
	docs := make([]doc, len(chunks))
	df := map[string]int{}
	total, vecs := 0, 0
	for i, c := range chunks {
		toks := tokenize(c.DocPath + " " + c.DocTitle + " " + c.Heading + " " + c.Content)
		tf := map[string]int{}
		for _, t := range toks {
			tf[t]++
		}
		for t := range tf {
			df[t]++
		}
		docs[i] = doc{chunk: c, tf: tf, len: len(toks)}
		total += len(toks)
		if c.Vec != nil {
			vecs++
		}
	}
	avg := 1.0
	if len(docs) > 0 {
		avg = float64(total) / float64(len(docs))
	}
	ix.mu.Lock()
	ix.docs, ix.df, ix.avgLen, ix.vecCount = docs, df, avg, vecs
	ix.mu.Unlock()
	return nil
}

func (ix *Index) noteErr(err error) {
	ix.mu.Lock()
	first := ix.lastErr == ""
	ix.lastErr, ix.errAt = err.Error(), time.Now()
	ix.mu.Unlock()
	if first {
		ix.log.Warn("嵌入服务不可用，检索退回纯关键词", "err", err)
	}
}

func (ix *Index) clearErr() {
	ix.mu.Lock()
	recovered := ix.lastErr != ""
	ix.lastErr, ix.errAt = "", time.Time{}
	ix.mu.Unlock()
	if recovered {
		ix.log.Info("嵌入服务已恢复")
	}
}

// queryVec 向量化问句；没有嵌入服务、还没有任何向量或调用失败时返回 nil（只走关键词）。
func (ix *Index) queryVec(ctx context.Context, query string) []float32 {
	emb, _ := ix.embedder()
	ix.mu.RLock()
	ready := ix.vecCount > 0
	ix.mu.RUnlock()
	if emb == nil || !ready {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, queryEmbedTimeout)
	defer cancel()
	vs, err := emb.Embed(ctx, []string{query})
	if err != nil {
		ix.noteErr(err)
		return nil
	}
	ix.clearErr()
	return vs[0]
}

// Search 返回最相关的 k 块。关键词一路用相对阈值过滤弱相关，向量一路用余弦下限过滤；
// 只有关键词一路时 Score 就是 BM25 分，两路都有时 Score 是 RRF 融合分。
func (ix *Index) Search(ctx context.Context, query string, k int) []Hit {
	qv := ix.queryVec(ctx, query)
	_, minScore := ix.embedder()

	ix.mu.RLock()
	defer ix.mu.RUnlock()
	if len(ix.docs) == 0 {
		return nil
	}
	kw := ix.keywordRank(query)
	var vec []int
	cos := map[int]float64{}
	if qv != nil {
		for i, d := range ix.docs {
			if len(d.chunk.Vec) != len(qv) {
				continue
			}
			if c := dot(qv, d.chunk.Vec); c >= minScore {
				cos[i] = c
				vec = append(vec, i)
			}
		}
		sort.Slice(vec, func(a, b int) bool { return cos[vec[a]] > cos[vec[b]] })
	}

	bm := map[int]float64{}
	for _, r := range kw {
		bm[r.i] = r.score
	}
	fused := map[int]float64{}
	order := []int{}
	add := func(i int, s float64) {
		if _, ok := fused[i]; !ok {
			order = append(order, i)
		}
		fused[i] += s
	}
	for rank, r := range kw {
		if qv == nil {
			add(r.i, r.score)
		} else {
			add(r.i, 1/float64(rrfK+rank+1))
		}
	}
	for rank, i := range vec {
		add(i, 1/float64(rrfK+rank+1))
	}
	sort.SliceStable(order, func(a, b int) bool { return fused[order[a]] > fused[order[b]] })
	if len(order) > k {
		order = order[:k]
	}
	hits := make([]Hit, len(order))
	for n, i := range order {
		c := ix.docs[i].chunk
		hits[n] = Hit{ChunkID: c.ID, DocumentID: c.DocumentID, DocPath: c.DocPath, DocTitle: c.DocTitle, Heading: c.Heading,
			Content: c.Content, Score: fused[i], Keyword: bm[i], Vector: cos[i], ReflectionID: c.ReflectionID}
	}
	return hits
}

type ranked struct {
	i     int
	score float64
}

// keywordRank BM25 排序，去掉低于最高分 30% 的噪声（只撞上一个「价格」之类常见二元组的块）。调用方持读锁。
func (ix *Index) keywordRank(query string) []ranked {
	qt := map[string]bool{}
	for _, t := range tokenize(query) {
		qt[t] = true
	}
	if len(qt) == 0 {
		return nil
	}
	n := float64(len(ix.docs))
	out := []ranked{}
	for i, d := range ix.docs {
		score := 0.0
		for t := range qt {
			f := d.tf[t]
			if f == 0 {
				continue
			}
			idf := math.Log(1 + (n-float64(ix.df[t])+0.5)/(float64(ix.df[t])+0.5))
			score += idf * float64(f) * (bm25K1 + 1) / (float64(f) + bm25K1*(1-bm25B+bm25B*float64(d.len)/ix.avgLen))
		}
		if score > 0 {
			out = append(out, ranked{i, score})
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].score > out[b].score })
	if len(out) > 0 {
		floor := out[0].score * 0.3
		for i, r := range out {
			if r.score < floor {
				return out[:i]
			}
		}
	}
	return out
}

func dot(a, b []float32) float64 {
	var s float32
	for i := range a {
		s += a[i] * b[i]
	}
	return float64(s)
}

func (ix *Index) Size() int {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return len(ix.docs)
}

func (ix *Index) Status() EmbedStatus {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	st := EmbedStatus{Enabled: ix.emb != nil, Model: model(ix.emb), Total: len(ix.docs), Embedded: ix.vecCount, LastError: ix.lastErr}
	if !ix.errAt.IsZero() {
		at := ix.errAt
		st.LastErrorAt = &at
	}
	return st
}

// ── 后台向量化 ──

// RunEmbedder 常驻任务：被 Kick 唤醒或每分钟一次，把缺向量的块分批送去向量化并回填，完成后重建快照。
// 嵌入服务不可用时本轮放弃，下轮重试；期间检索照常走关键词。
func (ix *Index) RunEmbedder(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		if n := ix.embedPending(ctx); n > 0 {
			ix.log.Info("知识库向量化完成", "count", n)
			if err := ix.rebuild(ctx); err != nil {
				ix.log.Error("重建知识库索引失败", "err", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ix.kick:
		case <-t.C:
		}
	}
}

func (ix *Index) embedPending(ctx context.Context) int {
	done := 0
	for ctx.Err() == nil {
		emb, _ := ix.embedder()
		if emb == nil {
			return done
		}
		items, err := ix.st.PendingEmbeddings(ctx, emb.Model(), embedBatch)
		if err != nil {
			ix.log.Error("读取待向量化条目失败", "err", err)
			return done
		}
		if len(items) == 0 {
			return done
		}
		texts := make([]string, len(items))
		for i, it := range items {
			texts[i] = it.Text
		}
		vecs, err := emb.Embed(ctx, texts)
		if err != nil {
			ix.noteErr(err)
			return done
		}
		ix.clearErr()
		if err := ix.st.SaveEmbeddings(ctx, emb.Model(), items, vecs); err != nil {
			ix.log.Error("回填向量失败", "err", err)
			return done
		}
		done += len(items)
	}
	return done
}

// ── 文本相似度（反思沉淀去重用）──
//
// 没有向量，判重用 bigram 集合的 Jaccard：同一知识换个说法通常仍共享大部分二字组。

func tokenSet(s string) map[string]bool {
	set := map[string]bool{}
	for _, t := range tokenize(s) {
		set[t] = true
	}
	return set
}

// Jaccard 两段文本分词集合的 Jaccard 相似度（0~1）。
func Jaccard(a, b string) float64 {
	sa, sb := tokenSet(a), tokenSet(b)
	if len(sa) == 0 || len(sb) == 0 {
		return 0
	}
	inter := 0
	for t := range sa {
		if sb[t] {
			inter++
		}
	}
	return float64(inter) / float64(len(sa)+len(sb)-inter)
}

func normalize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// NearlySame 规范化后相等、互相包含（都 ≥8 字），或 Jaccard ≥ 0.72。
func NearlySame(a, b string) bool {
	na, nb := normalize(a), normalize(b)
	if na == "" || nb == "" {
		return false
	}
	if na == nb {
		return true
	}
	if len([]rune(na)) >= 8 && len([]rune(nb)) >= 8 && (strings.Contains(na, nb) || strings.Contains(nb, na)) {
		return true
	}
	return Jaccard(a, b) >= 0.72
}

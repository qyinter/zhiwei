package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/http"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"golang.org/x/text/encoding/simplifiedchinese"

	"zhiwei/internal/embed"
	"zhiwei/internal/kb"
	"zhiwei/internal/settings"
	"zhiwei/internal/store"
)

// ── 知识库 ──

const (
	maxDocBytes    = 2 << 20
	maxPathRunes   = 255
	maxTitleRunes  = 200
	importMaxBytes = 50 << 20
	importMaxDocs  = 1000
)

func (s *Server) listKB(c *gin.Context) {
	list, err := s.st.ListKBDocuments(c)
	if err != nil {
		internal(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"list": list, "embedding": s.rt.Index.Status()})
}

func (s *Server) getKB(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	d, err := s.st.KBDocument(c, id)
	if err != nil {
		storeErr(c, err)
		return
	}
	c.JSON(http.StatusOK, d)
}

// checkDoc 校验目录 / 标题 / 正文，返回给用户看的错误文案。
func checkDoc(p, title string, size int) string {
	switch {
	case title == "" || utf8.RuneCountInString(title) > maxTitleRunes:
		return "标题不能为空且不超过 200 字"
	case utf8.RuneCountInString(p) > maxPathRunes:
		return "目录路径不超过 255 字"
	case size > maxDocBytes:
		return "单篇文档不超过 2MB"
	}
	return ""
}

func (s *Server) saveKB(c *gin.Context) {
	var req struct {
		Path    string `json:"path"`
		Title   string `json:"title"`
		Content string `json:"content"`
		Enabled *bool  `json:"enabled"`
	}
	if !bind(c, &req) {
		return
	}
	d := store.KBDocument{
		Path: store.CleanKBPath(req.Path), Title: strings.TrimSpace(req.Title), Content: req.Content,
		Enabled: req.Enabled == nil || *req.Enabled,
	}
	if c.Param("id") != "" {
		id, ok := idParam(c)
		if !ok {
			return
		}
		d.ID = id
	}
	if msg := checkDoc(d.Path, d.Title, len(d.Content)); msg != "" {
		fail(c, http.StatusBadRequest, msg)
		return
	}
	if err := s.st.SaveKBDocument(c, &d, kb.Split(d.Title, d.Content)); err != nil {
		storeErr(c, err)
		return
	}
	s.rebuildIndex(c)
	c.JSON(http.StatusOK, d)
}

func (s *Server) deleteKB(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	if err := s.st.DeleteKBDocument(c, id); err != nil {
		storeErr(c, err)
		return
	}
	s.rebuildIndex(c)
	c.Status(http.StatusNoContent)
}

func (s *Server) rebuildIndex(ctx context.Context) {
	if err := s.rt.Index.Rebuild(ctx); err != nil {
		s.log.Error("重建知识库索引失败", "err", err)
	}
}

func (s *Server) searchKB(c *gin.Context) {
	var req struct {
		Query string `json:"query"`
		TopK  int    `json:"topK"`
	}
	if !bind(c, &req) {
		return
	}
	if req.TopK <= 0 || req.TopK > 20 {
		req.TopK = 5
	}
	c.JSON(http.StatusOK, gin.H{"list": s.rt.Index.Search(c, req.Query, req.TopK)})
}

func (s *Server) renameKBFolder(c *gin.Context) {
	var req struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if !bind(c, &req) {
		return
	}
	from, to := store.CleanKBPath(req.From), store.CleanKBPath(req.To)
	if from == "" {
		fail(c, http.StatusBadRequest, "请选择要改名的目录")
		return
	}
	if utf8.RuneCountInString(to) > maxPathRunes {
		fail(c, http.StatusBadRequest, "目录路径不超过 255 字")
		return
	}
	n, err := s.st.RenameKBFolder(c, from, to)
	if err != nil {
		internal(c, err)
		return
	}
	s.rebuildIndex(c)
	c.JSON(http.StatusOK, gin.H{"documents": n})
}

// ── 批量导入 ──

// importExts 可导入的文本格式；.zip 会展开后按同样规则挑文件。
var importExts = map[string]bool{".md": true, ".markdown": true, ".txt": true}

type ignoredFile struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// importKB multipart：files（可多选，含 .zip）+ paths（与 files 一一对应的相对路径，
// 浏览器选文件夹时带上目录结构）+ prefix（导入到哪个目录下）+ mode（overwrite / skip 同名）。
// 文件所在子目录 → 文档目录，文件名去扩展名 → 标题。
func (s *Server) importKB(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, importMaxBytes)
	form, err := c.MultipartForm()
	if err != nil {
		fail(c, http.StatusBadRequest, "上传失败：单次导入不超过 50MB")
		return
	}
	prefix := store.CleanKBPath(first(form.Value["prefix"]))
	overwrite := first(form.Value["mode"]) != "skip"
	paths := form.Value["paths"]

	var (
		docs    []store.ImportDoc
		index   = map[string]int{}
		ignored = []ignoredFile{}
	)
	add := func(rel string, data []byte) {
		rel = strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(rel, "\\", "/")), "/")
		ext := strings.ToLower(path.Ext(rel))
		for _, seg := range strings.Split(rel, "/") {
			if strings.HasPrefix(seg, ".") || seg == "__MACOSX" {
				return // 隐藏文件与 macOS 打包残留，静默跳过
			}
		}
		if !importExts[ext] {
			ignored = append(ignored, ignoredFile{rel, "不支持的格式（只导入 .md / .markdown / .txt）"})
			return
		}
		content, ok := decodeText(data)
		if !ok {
			ignored = append(ignored, ignoredFile{rel, "无法识别的文本编码（请存为 UTF-8）"})
			return
		}
		dir, base := path.Split(rel)
		d := store.ImportDoc{Path: store.CleanKBPath(prefix + "/" + dir), Title: strings.TrimSpace(strings.TrimSuffix(base, path.Ext(base))), Content: content}
		if msg := checkDoc(d.Path, d.Title, len(content)); msg != "" {
			ignored = append(ignored, ignoredFile{rel, msg})
			return
		}
		if strings.TrimSpace(content) == "" {
			ignored = append(ignored, ignoredFile{rel, "空文件"})
			return
		}
		d.Chunks = kb.Split(d.Title, d.Content)
		key := d.Path + "\x00" + d.Title
		if i, dup := index[key]; dup {
			docs[i] = d // 同一批里重名，以后出现的为准
			return
		}
		index[key] = len(docs)
		docs = append(docs, d)
	}

	for i, fh := range form.File["files"] {
		rel := fh.Filename
		if i < len(paths) && strings.TrimSpace(paths[i]) != "" {
			rel = paths[i]
		}
		data, err := readUpload(fh.Open)
		if err != nil {
			ignored = append(ignored, ignoredFile{rel, "读取失败"})
			continue
		}
		if strings.ToLower(path.Ext(rel)) != ".zip" {
			add(rel, data)
			continue
		}
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			ignored = append(ignored, ignoredFile{rel, "不是有效的 zip 压缩包"})
			continue
		}
		for _, f := range zr.File {
			if f.FileInfo().IsDir() {
				continue
			}
			name := f.Name
			// zip 规范里没打 UTF-8 标记的文件名编码不确定：macOS 打的包其实是 UTF-8，
			// Windows 资源管理器打的包是 GBK。合法 UTF-8 就原样用，否则按 GBK 解。
			if !utf8.ValidString(name) {
				if n, err := simplifiedchinese.GBK.NewDecoder().String(name); err == nil && !strings.ContainsRune(n, utf8.RuneError) {
					name = n
				}
			}
			if f.UncompressedSize64 > maxDocBytes {
				ignored = append(ignored, ignoredFile{name, "单篇文档不超过 2MB"})
				continue
			}
			data, err := readUpload(func() (io.ReadCloser, error) { return f.Open() })
			if err != nil {
				ignored = append(ignored, ignoredFile{name, "解压失败"})
				continue
			}
			add(name, data)
		}
	}
	if len(docs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "没有可导入的文档", "ignored": ignored})
		return
	}
	if len(docs) > importMaxDocs {
		fail(c, http.StatusBadRequest, "单次最多导入 1000 篇文档，请分批导入")
		return
	}
	res, err := s.st.ImportKBDocuments(c, docs, overwrite)
	if err != nil {
		internal(c, err)
		return
	}
	s.rebuildIndex(c)
	c.JSON(http.StatusOK, gin.H{"created": res.Created, "updated": res.Updated, "skipped": res.Skipped, "ignored": ignored})
}

func first(v []string) string {
	if len(v) == 0 {
		return ""
	}
	return v[0]
}

func readUpload[T io.ReadCloser](open func() (T, error)) ([]byte, error) {
	r, err := open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(io.LimitReader(r, importMaxBytes))
}

// decodeText UTF-8（去 BOM）原样返回；不是 UTF-8 时按 GBK 再试一次（Windows 记事本的老默认）。
func decodeText(b []byte) (string, bool) {
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	if utf8.Valid(b) {
		return string(b), true
	}
	// GBK 解码器遇到非法字节会替换成 U+FFFD 而不报错，出现替换符就当识别失败
	if s, err := simplifiedchinese.GBK.NewDecoder().Bytes(b); err == nil && !bytes.ContainsRune(s, utf8.RuneError) {
		return string(s), true
	}
	return "", false
}

// ── 向量检索测试 ──

func (s *Server) settingsEmbeddingTest(c *gin.Context) {
	var req settings.Embedding
	if !bind(c, &req) {
		return
	}
	req.APIKey = settings.All{Embedding: req}.Unmask(s.rt.Settings.Get()).Embedding.APIKey
	if !strings.HasPrefix(req.BaseURL, "http://") && !strings.HasPrefix(req.BaseURL, "https://") {
		fail(c, http.StatusBadRequest, "嵌入服务地址需以 http:// 或 https:// 开头")
		return
	}
	ctx, cancel := context.WithTimeout(c, 15*time.Second)
	defer cancel()
	start := time.Now()
	vecs, err := embed.New(req).Embed(ctx, []string{"怎么充值", "充值方法：在控制台充值页选择金额", "API Key 在哪里创建"})
	if err != nil {
		fail(c, http.StatusBadGateway, err.Error())
		return
	}
	dot := func(a, b []float32) float64 {
		var s float64
		for i := range a {
			s += float64(a[i] * b[i])
		}
		return s
	}
	c.JSON(http.StatusOK, gin.H{
		"latencyMs": time.Since(start).Milliseconds(), "dims": len(vecs[0]),
		"related": dot(vecs[0], vecs[1]), "unrelated": dot(vecs[0], vecs[2]),
	})
}

package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"golang.org/x/text/encoding/simplifiedchinese"

	"zhiwei/internal/agent"
	"zhiwei/internal/skill"
	"zhiwei/internal/store"
)

// ── 技能 ──

const importMaxSkills = 100

func (s *Server) reloadSkills(ctx context.Context) {
	if err := s.rt.Agent.ReloadSkills(ctx); err != nil {
		s.log.Error("重新加载技能失败", "err", err)
	}
}

func (s *Server) listSkills(c *gin.Context) {
	list, err := s.st.ListSkills(c)
	if err != nil {
		internal(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"builtin": agent.BuiltinTools(), "list": list, "template": skill.Template})
}

func (s *Server) getSkill(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	k, err := s.st.Skill(c, id)
	if err != nil {
		storeErr(c, err)
		return
	}
	c.JSON(http.StatusOK, k)
}

// saveSkill 新建 / 编辑 SKILL.md（name、description 从 frontmatter 解析）；编辑不动附属文件。
func (s *Server) saveSkill(c *gin.Context) {
	var req struct {
		Content string `json:"content"`
		Enabled *bool  `json:"enabled"`
	}
	if !bind(c, &req) {
		return
	}
	if len(req.Content) > skill.MaxContentSize {
		fail(c, http.StatusBadRequest, "SKILL.md 不超过 100KB")
		return
	}
	m, err := skill.Parse(req.Content, "")
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	k := store.Skill{Name: m.Name, Description: m.Description, Content: req.Content, Enabled: req.Enabled == nil || *req.Enabled}
	if c.Param("id") != "" {
		id, ok := idParam(c)
		if !ok {
			return
		}
		k.ID = id
	}
	var files []store.SkillFile
	if k.ID == 0 {
		files = []store.SkillFile{}
	}
	if err := s.st.SaveSkill(c, &k, files); err != nil {
		if errors.Is(err, store.ErrSkillExists) {
			fail(c, http.StatusConflict, fmt.Sprintf("已有同名技能「%s」", k.Name))
			return
		}
		storeErr(c, err)
		return
	}
	s.reloadSkills(c)
	c.JSON(http.StatusOK, k)
}

func (s *Server) toggleSkill(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !bind(c, &req) {
		return
	}
	if err := s.st.SetSkillEnabled(c, id, req.Enabled); err != nil {
		storeErr(c, err)
		return
	}
	s.reloadSkills(c)
	c.Status(http.StatusNoContent)
}

func (s *Server) deleteSkill(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	if err := s.st.DeleteSkill(c, id); err != nil {
		storeErr(c, err)
		return
	}
	s.reloadSkills(c)
	c.Status(http.StatusNoContent)
}

// importSkills multipart：files（技能文件夹里的文件 / .zip / 单个 .md）+ paths（与 files 一一对应的相对路径）+ mode。
//
// 含 SKILL.md 的目录即一个技能，目录下其余文本文件作为附属文件（路径相对该目录）；
// 不在任何技能目录里、但开头带 name / description frontmatter 的 .md 单独成为一个技能。
func (s *Server) importSkills(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, importMaxBytes)
	form, err := c.MultipartForm()
	if err != nil {
		fail(c, http.StatusBadRequest, "上传失败：单次导入不超过 50MB")
		return
	}
	overwrite := first(form.Value["mode"]) != "skip"
	paths := form.Value["paths"]

	type entry struct {
		rel  string
		data []byte
	}
	var (
		entries []entry
		ignored = []ignoredFile{}
	)
	add := func(rel string, data []byte) {
		if p := skill.CleanFilePath(rel); p != "" {
			entries = append(entries, entry{p, data})
		}
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
		// 压缩包内的条目挂在「包名/」下：SKILL.md 直接放在包根目录时，用包名兜底技能名
		base := strings.TrimSuffix(path.Base(rel), path.Ext(rel))
		for _, f := range zr.File {
			if f.FileInfo().IsDir() {
				continue
			}
			name := f.Name
			if !utf8.ValidString(name) {
				if n, err := simplifiedchinese.GBK.NewDecoder().String(name); err == nil && !strings.ContainsRune(n, utf8.RuneError) {
					name = n
				}
			}
			if f.UncompressedSize64 > skill.MaxFileSize {
				ignored = append(ignored, ignoredFile{name, "单个文件不超过 200KB"})
				continue
			}
			data, err := readUpload(func() (io.ReadCloser, error) { return f.Open() })
			if err != nil {
				ignored = append(ignored, ignoredFile{name, "解压失败"})
				continue
			}
			add(base+"/"+name, data)
		}
	}

	// 技能根目录 = 每个 SKILL.md 所在目录
	roots := map[string]string{} // 根目录 → SKILL.md 的相对路径
	for _, e := range entries {
		if strings.EqualFold(path.Base(e.rel), "SKILL.md") {
			roots[dirOf(e.rel)] = e.rel
		}
	}
	rootOf := func(rel string) (string, bool) {
		best, found := "", false
		for r := range roots {
			if (r == "" || strings.HasPrefix(rel, r+"/")) && (!found || len(r) > len(best)) {
				best, found = r, true
			}
		}
		return best, found
	}

	type pending struct {
		skill store.ImportSkill
		size  int
		src   string
	}
	var (
		list   []*pending
		byRoot = map[string]*pending{}
	)
	for root, rel := range roots {
		byRoot[root] = &pending{src: rel}
	}
	// 先解析所有 SKILL.md，再分配附属文件（大小合计要算上 SKILL.md）
	for _, e := range entries {
		if !strings.EqualFold(path.Base(e.rel), "SKILL.md") {
			continue
		}
		dir := dirOf(e.rel)
		text, ok := decodeText(e.data)
		reason := ""
		var m skill.Meta
		switch {
		case !ok:
			reason = "无法识别的文本编码（请存为 UTF-8）"
		case len(e.data) > skill.MaxContentSize:
			reason = "SKILL.md 不超过 100KB"
		default:
			fallback := ""
			if dir != "" {
				fallback = path.Base(dir)
			}
			if m, err = skill.Parse(text, fallback); err != nil {
				reason = err.Error()
			}
		}
		if reason != "" {
			ignored = append(ignored, ignoredFile{e.rel, reason})
			delete(byRoot, dir)
			continue
		}
		p := byRoot[dir]
		p.skill.Name, p.skill.Description, p.skill.Content = m.Name, m.Description, text
		p.size += len(e.data)
	}
	for _, e := range entries {
		if strings.EqualFold(path.Base(e.rel), "SKILL.md") {
			continue
		}
		ext := strings.ToLower(path.Ext(e.rel))
		root, inSkill := rootOf(e.rel)
		if !inSkill {
			// 不在技能目录里：带 frontmatter 的单个 .md 也算一个技能
			if ext == ".md" || ext == ".markdown" {
				text, ok := decodeText(e.data)
				if ok && len(e.data) <= skill.MaxContentSize {
					m, err := skill.Parse(text, strings.TrimSuffix(path.Base(e.rel), path.Ext(e.rel)))
					if err == nil {
						list = append(list, &pending{skill: store.ImportSkill{Name: m.Name, Description: m.Description, Content: text}, size: len(e.data), src: e.rel})
						continue
					}
				}
			}
			ignored = append(ignored, ignoredFile{e.rel, "不在任何技能目录里（目录下需要有 SKILL.md，或文件开头写 name / description）"})
			continue
		}
		p := byRoot[root]
		if p == nil {
			continue // 所属技能的 SKILL.md 已判为无效，上面记过原因
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(e.rel, root), "/")
		var reason string
		text, ok := decodeText(e.data)
		switch {
		case skill.ScriptExts[ext]:
			reason = "脚本不会执行，已跳过"
		case !skill.FileExts[ext]:
			reason = "不支持的格式（附属文件只收 .md / .txt / .json / .yaml / .csv）"
		case len(e.data) > skill.MaxFileSize:
			reason = "单个文件不超过 200KB"
		case !ok:
			reason = "无法识别的文本编码（请存为 UTF-8）"
		case len(p.skill.Files) >= skill.MaxFiles:
			reason = "单个技能最多 50 个附属文件"
		case p.size+len(e.data) > skill.MaxTotalSize:
			reason = "单个技能的文件合计不超过 1MB"
		}
		if reason != "" {
			ignored = append(ignored, ignoredFile{e.rel, reason})
			continue
		}
		p.skill.Files = append(p.skill.Files, store.SkillFile{Path: rel, Content: text})
		p.size += len(e.data)
	}
	for _, p := range byRoot {
		list = append(list, p)
	}

	// 同一批里重名，路径靠后的为准
	sort.Slice(list, func(i, j int) bool { return list[i].src < list[j].src })
	seen := map[string]int{}
	var out []store.ImportSkill
	for _, p := range list {
		sort.Slice(p.skill.Files, func(i, j int) bool { return p.skill.Files[i].Path < p.skill.Files[j].Path })
		if i, dup := seen[p.skill.Name]; dup {
			out[i] = p.skill
			continue
		}
		seen[p.skill.Name] = len(out)
		out = append(out, p.skill)
	}
	if len(out) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "没有找到可导入的技能（需要 SKILL.md）", "ignored": ignored})
		return
	}
	if len(out) > importMaxSkills {
		fail(c, http.StatusBadRequest, "单次最多导入 100 个技能，请分批导入")
		return
	}
	res, err := s.st.ImportSkills(c, out, overwrite)
	if err != nil {
		internal(c, err)
		return
	}
	s.reloadSkills(c)
	names := make([]string, len(out))
	for i, k := range out {
		names[i] = k.Name
	}
	c.JSON(http.StatusOK, gin.H{"created": res.Created, "updated": res.Updated, "skipped": res.Skipped, "ignored": ignored, "names": names})
}

func dirOf(rel string) string {
	d := path.Dir(rel)
	if d == "." {
		return ""
	}
	return d
}

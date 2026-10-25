// Package skill 技能包：沿用 Claude Agent Skills 的 SKILL.md 格式（YAML frontmatter 的 name / description + Markdown 正文），
// 外加若干附属参考文件。
//
// 渐进加载：system prompt 只列「名称：适用场景」，模型判断用得上时再调 read_skill 读正文与附属文件，
// 技能再多也不会撑大每一轮的上下文。知微不执行技能里的脚本——只有文字说明会交给模型。
package skill

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const (
	MaxNameRunes   = 64
	MaxDescRunes   = 1024
	MaxContentSize = 100 << 10 // SKILL.md
	MaxFileSize    = 200 << 10 // 单个附属文件
	MaxFiles       = 50
	MaxTotalSize   = 1 << 20 // 单个技能全部文本
)

// FileExts 可作为附属文件的文本格式。
var FileExts = map[string]bool{
	".md": true, ".markdown": true, ".txt": true, ".json": true,
	".yaml": true, ".yml": true, ".csv": true, ".tsv": true,
}

// ScriptExts 脚本：不执行也不存，导入时明确提示。
var ScriptExts = map[string]bool{
	".js": true, ".mjs": true, ".cjs": true, ".ts": true, ".py": true, ".sh": true,
	".bash": true, ".rb": true, ".go": true, ".php": true, ".ps1": true, ".bat": true,
}

// Skill 内存里的启用技能（注入 Brain）。
type Skill struct {
	ID          int64
	Name        string
	Description string
	Body        string            // 去掉 frontmatter 的正文
	Files       map[string]string // 相对技能根目录的路径 → 内容
}

var reName = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N}_.-]*$`)

// Meta SKILL.md 解析结果。
type Meta struct {
	Name        string
	Description string
	Body        string
}

var errNoFrontmatter = errors.New("缺少 frontmatter：文件开头需要用 --- 包住 name 与 description")

// Parse 解析 SKILL.md。fallbackName 用于 frontmatter 没写 name 时（取所在目录名）。
func Parse(content, fallbackName string) (Meta, error) {
	fm, body, ok := splitFrontmatter(content)
	if !ok {
		return Meta{}, errNoFrontmatter
	}
	var raw struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal([]byte(fm), &raw); err != nil {
		return Meta{}, fmt.Errorf("frontmatter 不是合法的 YAML：%s", firstLine(err.Error()))
	}
	m := Meta{Name: strings.TrimSpace(raw.Name), Description: strings.Join(strings.Fields(raw.Description), " "), Body: strings.TrimSpace(body)}
	if m.Name == "" {
		m.Name = strings.TrimSpace(fallbackName)
	}
	return m, m.Validate()
}

func (m Meta) Validate() error {
	switch {
	case m.Name == "":
		return errors.New("frontmatter 缺少 name")
	case utf8.RuneCountInString(m.Name) > MaxNameRunes || !reName.MatchString(m.Name):
		return errors.New("name 只能用字母、数字、中文与 - _ .，不超过 64 字")
	case m.Description == "":
		return errors.New("frontmatter 缺少 description（模型靠它判断什么时候用这个技能）")
	case utf8.RuneCountInString(m.Description) > MaxDescRunes:
		return errors.New("description 不超过 1024 字")
	case m.Body == "":
		return errors.New("SKILL.md 正文为空")
	}
	return nil
}

// splitFrontmatter 拆出开头 --- … --- 之间的 YAML。
func splitFrontmatter(s string) (fm, body string, ok bool) {
	s = strings.TrimPrefix(strings.ReplaceAll(s, "\r\n", "\n"), "\ufeff")
	s = strings.TrimLeft(s, "\n")
	if !strings.HasPrefix(s, "---\n") {
		return "", "", false
	}
	rest := s[4:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", "", false
	}
	after := rest[end+4:]
	if nl := strings.IndexByte(after, '\n'); nl >= 0 {
		if strings.TrimSpace(after[:nl]) != "" {
			return "", "", false
		}
		after = after[nl+1:]
	} else if strings.TrimSpace(after) != "" {
		return "", "", false
	}
	return rest[:end], after, true
}

// CleanFilePath 规范化附属文件路径；越界（..）、绝对路径、隐藏文件返回空串。
func CleanFilePath(p string) string {
	p = strings.ReplaceAll(strings.TrimSpace(p), "\\", "/")
	if strings.HasPrefix(p, "/") {
		return ""
	}
	p = path.Clean(p)
	if p == "." || p == ".." || strings.HasPrefix(p, "../") {
		return ""
	}
	for _, seg := range strings.Split(p, "/") {
		if strings.HasPrefix(seg, ".") || seg == "__MACOSX" {
			return ""
		}
	}
	return p
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// Template 后台「新建技能」的初始内容。
const Template = `---
name: refund-policy
description: 退款规则与流程。客户问能不能退款、怎么退、多久到账时使用。
---

# 退款处理

## 适用范围
（写清楚这个技能管哪些问题。）

## 回答要点
1. （分步骤写模型应该怎么答、先确认什么。）
2.

## 口径与禁忌
- （必须遵守的规则，例如金额、时限的说法；不能承诺的事。）
`

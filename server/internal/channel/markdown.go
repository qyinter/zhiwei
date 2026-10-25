package channel

// 回复用的精简 Markdown（模型按 system prompt 输出、原样落库），由各渠道发送前渲染：
//   - QQ 群聊 / 单聊 → QQ Markdown 消息（RenderQQ）
//   - Telegram       → parse_mode=HTML（RenderTelegramHTML）
//   - 其他场景        → 纯文本（RenderPlain）
//
// 支持的子集：段落、**加粗**、~~删除线~~、`行内代码`、``` 代码块、[文字](链接)、
// 「- 」/「1. 」列表（不嵌套）、「> 」引用；标题一律降级为加粗行，分割线丢弃，表格按原文。
// 只做行级解析，不追求完整 CommonMark —— 输入受 prompt 约束，异常输入宁可原样输出。

import (
	"html"
	"regexp"
	"strconv"
	"strings"
)

var (
	reFence    = regexp.MustCompile("^\\s*```")
	reHeading  = regexp.MustCompile(`^\s{0,3}#{1,6}\s+(.*?)\s*#*\s*$`)
	reRule     = regexp.MustCompile(`^\s{0,3}([-*_])(\s*[-*_]){2,}\s*$`)
	reBullet   = regexp.MustCompile(`^\s*[-*+]\s+(.*)$`)
	reOrdered  = regexp.MustCompile(`^\s*(\d{1,3})[.)]\s+(.*)$`)
	reQuote    = regexp.MustCompile(`^\s{0,3}>\s?(.*)$`)
	reCode     = regexp.MustCompile("`([^`\n]+)`")
	reLink     = regexp.MustCompile(`\[([^\]\n]+)\]\((https?://[^\s)]+)\)`)
	reBold     = regexp.MustCompile(`\*\*([^*\n]+?)\*\*|__([^_\n]+?)__`)
	reStrike   = regexp.MustCompile(`~~([^~\n]+?)~~`)
	reBlankRun = regexp.MustCompile(`\n{3,}`)
)

type blockKind int

const (
	blkPara blockKind = iota
	blkHeading
	blkBullet
	blkOrdered
	blkQuote
	blkCode
	blkBlank
)

type mdLine struct {
	kind blockKind
	num  string // 有序列表序号
	text string // 去掉标记后的内容；代码块为原文
}

// parseLines 把文本切成行级结构；代码块内的行原样保留，分割线丢弃。
func parseLines(src string) []mdLine {
	var out []mdLine
	inCode := false
	for _, ln := range strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n") {
		if reFence.MatchString(ln) {
			inCode = !inCode
			if inCode {
				out = append(out, mdLine{kind: blkBlank})
			}
			continue
		}
		if inCode {
			out = append(out, mdLine{kind: blkCode, text: ln})
			continue
		}
		switch {
		case strings.TrimSpace(ln) == "":
			out = append(out, mdLine{kind: blkBlank})
		case reRule.MatchString(ln):
			out = append(out, mdLine{kind: blkBlank})
		case reHeading.MatchString(ln):
			out = append(out, mdLine{kind: blkHeading, text: reHeading.FindStringSubmatch(ln)[1]})
		case reQuote.MatchString(ln):
			out = append(out, mdLine{kind: blkQuote, text: reQuote.FindStringSubmatch(ln)[1]})
		case reBullet.MatchString(ln):
			out = append(out, mdLine{kind: blkBullet, text: reBullet.FindStringSubmatch(ln)[1]})
		case reOrdered.MatchString(ln):
			m := reOrdered.FindStringSubmatch(ln)
			out = append(out, mdLine{kind: blkOrdered, num: m[1], text: m[2]})
		default:
			out = append(out, mdLine{kind: blkPara, text: strings.TrimRight(ln, " \t")})
		}
	}
	return out
}

func isList(k blockKind) bool { return k == blkBullet || k == blkOrdered }

// joinBlocks 逐行输出，不同类型的块之间保证恰好一个空行（QQ 要求列表前空行，否则不识别）。
func joinBlocks(lines []mdLine, render func(l mdLine) string) string {
	var sb strings.Builder
	prev := blkBlank
	for _, l := range lines {
		if l.kind == blkBlank {
			if prev != blkBlank {
				sb.WriteString("\n")
			}
			prev = blkBlank
			continue
		}
		if prev != blkBlank {
			same := prev == l.kind || (isList(prev) && isList(l.kind))
			if same && l.kind != blkHeading {
				sb.WriteString("\n")
			} else {
				sb.WriteString("\n\n")
			}
		} else if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(render(l))
		prev = l.kind
	}
	return strings.TrimSpace(reBlankRun.ReplaceAllString(sb.String(), "\n\n"))
}

// RenderQQ 转成 QQ Markdown。QQ 不支持代码：行内代码去掉反引号，代码块改成引用块（每行一条，保留缩进）。
func RenderQQ(src string) string {
	return joinBlocks(parseLines(src), func(l mdLine) string {
		switch l.kind {
		case blkHeading:
			return "**" + stripBold(l.text) + "**"
		case blkBullet:
			return "- " + qqInline(l.text)
		case blkOrdered:
			return l.num + ". " + qqInline(l.text)
		case blkQuote:
			return "> " + qqInline(l.text)
		case blkCode:
			if strings.TrimSpace(l.text) == "" {
				return ">"
			}
			// 行首缩进换成不换行空格，否则 QQ 会吞掉（JSON / YAML 靠缩进看层级）
			body := strings.TrimLeft(l.text, " ")
			return "> " + strings.Repeat("\u00a0", len(l.text)-len(body)) + body
		}
		return qqInline(l.text)
	})
}

func qqInline(s string) string {
	return reCode.ReplaceAllString(s, "$1")
}

func stripBold(s string) string {
	return strings.NewReplacer("**", "", "__", "").Replace(s)
}

// RenderTelegramHTML 转成 Telegram 的 HTML 格式（parse_mode=HTML）：
// 加粗 <b>、删除线 <s>、代码 <code>/<pre>、链接 <a>、引用 <blockquote>；列表用「•」与序号文本。
func RenderTelegramHTML(src string) string {
	lines := parseLines(src)
	// 连续的代码行 / 引用行要合成一个 <pre> / <blockquote>，先按块分组再交给 joinBlocks
	var grouped []mdLine
	for _, l := range lines {
		n := len(grouped)
		if n > 0 && (l.kind == blkCode || l.kind == blkQuote) && grouped[n-1].kind == l.kind {
			grouped[n-1].text += "\n" + l.text
			continue
		}
		grouped = append(grouped, l)
	}
	return joinBlocks(grouped, func(l mdLine) string {
		switch l.kind {
		case blkHeading:
			return "<b>" + tgInline(stripBold(l.text)) + "</b>"
		case blkBullet:
			return "• " + tgInline(l.text)
		case blkOrdered:
			return l.num + ". " + tgInline(l.text)
		case blkQuote:
			return "<blockquote>" + tgInline(l.text) + "</blockquote>"
		case blkCode:
			return "<pre>" + html.EscapeString(strings.Trim(l.text, "\n")) + "</pre>"
		}
		return tgInline(l.text)
	})
}

// tgInline 先把行内代码抠出来占位（里面的 * 不能被当成加粗），转义后再套标签。
func tgInline(s string) string {
	var codes []string
	s = reCode.ReplaceAllStringFunc(s, func(m string) string {
		codes = append(codes, "<code>"+html.EscapeString(m[1:len(m)-1])+"</code>")
		return "\x00" + strconv.Itoa(len(codes)-1) + "\x00"
	})
	var links []string
	s = reLink.ReplaceAllStringFunc(s, func(m string) string {
		p := reLink.FindStringSubmatch(m)
		links = append(links, `<a href="`+html.EscapeString(p[2])+`">`+html.EscapeString(p[1])+"</a>")
		return "\x01" + strconv.Itoa(len(links)-1) + "\x01"
	})
	s = html.EscapeString(s)
	s = reBold.ReplaceAllStringFunc(s, func(m string) string {
		p := reBold.FindStringSubmatch(m)
		return "<b>" + p[1] + p[2] + "</b>"
	})
	s = reStrike.ReplaceAllString(s, "<s>$1</s>")
	for i, c := range codes {
		s = strings.Replace(s, "\x00"+strconv.Itoa(i)+"\x00", c, 1)
	}
	for i, l := range links {
		s = strings.Replace(s, "\x01"+strconv.Itoa(i)+"\x01", l, 1)
	}
	return s
}

// RenderPlain 去掉全部标记的纯文本（频道、Telegram HTML 解析失败时的兜底）。
func RenderPlain(src string) string {
	return joinBlocks(parseLines(src), func(l mdLine) string {
		switch l.kind {
		case blkBullet:
			return "• " + plainInline(l.text)
		case blkOrdered:
			return l.num + ". " + plainInline(l.text)
		case blkCode:
			return l.text
		}
		return plainInline(l.text)
	})
}

func plainInline(s string) string {
	s = reCode.ReplaceAllString(s, "$1")
	s = reLink.ReplaceAllString(s, "$1（$2）")
	s = reBold.ReplaceAllString(s, "$1$2")
	return reStrike.ReplaceAllString(s, "$1")
}

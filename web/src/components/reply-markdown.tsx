"use client"

// 机器人回复的精简 Markdown 预览，与服务端 channel/markdown.go 同一子集：
// 段落、**加粗**、~~删除线~~、`行内代码`、``` 代码块、[文字](链接)、- / 1. 列表、> 引用；标题按加粗行显示。
import { Fragment } from "react"

const INLINE = /(`[^`\n]+`|\*\*[^*\n]+?\*\*|~~[^~\n]+?~~|\[[^\]\n]+\]\(https?:\/\/[^\s)]+\))/g

function Inline({ text }: { text: string }) {
  return (
    <>
      {text.split(INLINE).map((p, i) => {
        if (p.startsWith("`") && p.endsWith("`") && p.length > 2)
          return (
            <code key={i} className="rounded bg-foreground/[0.07] px-1 py-0.5 font-mono text-[0.9em]">
              {p.slice(1, -1)}
            </code>
          )
        // 加粗 / 删除线里可能还套着行内代码、链接，递归渲染
        if (p.startsWith("**") && p.endsWith("**") && p.length > 4)
          return (
            <strong key={i}>
              <Inline text={p.slice(2, -2)} />
            </strong>
          )
        if (p.startsWith("~~") && p.endsWith("~~") && p.length > 4)
          return (
            <s key={i}>
              <Inline text={p.slice(2, -2)} />
            </s>
          )
        const link = /^\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)$/.exec(p)
        if (link)
          return (
            <a key={i} href={link[2]} target="_blank" rel="noreferrer" className="text-primary underline underline-offset-2">
              {link[1]}
            </a>
          )
        return <Fragment key={i}>{p}</Fragment>
      })}
    </>
  )
}

type Block =
  | { kind: "p" | "h"; lines: string[] }
  | { kind: "ul" | "ol"; items: { num?: string; text: string }[] }
  | { kind: "quote"; lines: string[] }
  | { kind: "code"; lines: string[] }

function parse(src: string): Block[] {
  const blocks: Block[] = []
  let code: string[] | null = null
  const last = () => blocks[blocks.length - 1]
  for (const raw of src.replace(/\r\n/g, "\n").split("\n")) {
    if (/^\s*```/.test(raw)) {
      if (code) {
        blocks.push({ kind: "code", lines: code })
        code = null
      } else code = []
      continue
    }
    if (code) {
      code.push(raw)
      continue
    }
    const line = raw.trimEnd()
    let m: RegExpExecArray | null
    if (!line.trim() || /^\s{0,3}([-*_])(\s*[-*_]){2,}\s*$/.test(line)) {
      blocks.push({ kind: "p", lines: [] })
    } else if ((m = /^\s{0,3}#{1,6}\s+(.*?)\s*#*$/.exec(line))) {
      blocks.push({ kind: "h", lines: [m[1]] })
    } else if ((m = /^\s{0,3}>\s?(.*)$/.exec(line))) {
      const b = last()
      if (b?.kind === "quote") b.lines.push(m[1])
      else blocks.push({ kind: "quote", lines: [m[1]] })
    } else if ((m = /^\s*[-*+]\s+(.*)$/.exec(line))) {
      const b = last()
      if (b?.kind === "ul") b.items.push({ text: m[1] })
      else blocks.push({ kind: "ul", items: [{ text: m[1] }] })
    } else if ((m = /^\s*(\d{1,3})[.)]\s+(.*)$/.exec(line))) {
      const b = last()
      if (b?.kind === "ol") b.items.push({ num: m[1], text: m[2] })
      else blocks.push({ kind: "ol", items: [{ num: m[1], text: m[2] }] })
    } else {
      const b = last()
      if (b?.kind === "p") b.lines.push(line)
      else blocks.push({ kind: "p", lines: [line] })
    }
  }
  if (code) blocks.push({ kind: "code", lines: code })
  return blocks.filter((b) => !("lines" in b) || b.lines.length > 0 || b.kind === "code")
}

/** 渲染机器人回复（体验台、会话记录共用），外层容器负责字号与配色 */
export function ReplyMarkdown({ source }: { source: string }) {
  return (
    <div className="grid gap-2 [overflow-wrap:anywhere]">
      {parse(source).map((b, i) => {
        switch (b.kind) {
          case "h":
            return (
              <p key={i} className="font-semibold">
                <Inline text={b.lines[0].replace(/\*\*/g, "")} />
              </p>
            )
          case "ul":
          case "ol":
            return (
              <ul key={i} className="grid gap-0.5">
                {b.items.map((it, j) => (
                  <li key={j} className="flex gap-1.5">
                    <span className="shrink-0 font-mono tabular-nums opacity-60">{b.kind === "ul" ? "•" : `${it.num}.`}</span>
                    <span className="min-w-0">
                      <Inline text={it.text} />
                    </span>
                  </li>
                ))}
              </ul>
            )
          case "quote":
            return (
              <blockquote key={i} className="border-l-2 border-current/25 pl-2.5 opacity-80">
                {b.lines.map((l, j) => (
                  <p key={j}>
                    <Inline text={l} />
                  </p>
                ))}
              </blockquote>
            )
          case "code":
            return (
              <pre key={i} className="overflow-x-auto rounded-lg bg-foreground/[0.06] px-2.5 py-2 font-mono text-[12px] leading-relaxed">
                {b.lines.join("\n")}
              </pre>
            )
          default:
            return (
              <p key={i}>
                {b.lines.map((l, j) => (
                  <Fragment key={j}>
                    {j > 0 && <br />}
                    <Inline text={l} />
                  </Fragment>
                ))}
              </p>
            )
        }
      })}
    </div>
  )
}

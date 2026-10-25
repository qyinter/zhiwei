// 设计书阅读脚本（FA-STD-001 v1.2）。正文 HTML 保持零脚本零 class，
// 所有阅读辅助在此运行时注入：
//   1. 顶栏（返回目录 + 当前文档名）
//   2. 标题锚点 id + 左侧滚动目录（h2/h3，带 scroll spy）
//   3. 正文里的「FA-DD-XXX / FA-STD-XXX」字样自动变为跳转链接（清单见 manifest.js）
//   4. 宽表格包一层横向滚动容器
/* global DOC_MANIFEST */
(function () {
  'use strict'

  const main = document.querySelector('main')
  if (main == null) return
  const isIndex = /(?:^|\/)index\.html?$/.test(location.pathname) || document.body.dataset.index === '1'

  // ── 1. 顶栏 ──
  const topbar = document.createElement('div')
  topbar.className = 'topbar'
  const h1 = main.querySelector('h1')
  topbar.innerHTML = (isIndex ? '' : '<a href="index.html">← 设计书目录</a>') +
    '<b>' + (h1 != null ? h1.textContent : document.title) + '</b>'
  document.body.prepend(topbar)

  // ── 2. 标题锚点 + 侧栏目录 ──
  const heads = [...main.querySelectorAll('h2, h3')]
  const used = new Set()
  for (const h of heads) {
    let id = h.textContent.trim().replace(/\s+/g, '-').replace(/[#?&%"'<>]/g, '')
    while (used.has(id)) id += '-'
    used.add(id)
    h.id = id
  }
  if (!isIndex && heads.length >= 3) {
    const toc = document.createElement('nav')
    toc.className = 'toc'
    toc.innerHTML = heads
      .map((h) => `<a class="${h.tagName === 'H3' ? 'h3' : ''}" href="#${h.id}">${h.textContent}</a>`)
      .join('')
    document.body.append(toc)
    document.body.classList.add('has-toc')

    const links = new Map(heads.map((h, i) => [h.id, toc.children[i]]))
    let current = null
    const spy = new IntersectionObserver(
      (entries) => {
        for (const e of entries) {
          if (e.isIntersecting) {
            if (current != null) current.classList.remove('on')
            current = links.get(e.target.id)
            if (current != null) current.classList.add('on')
          }
        }
      },
      { rootMargin: '-72px 0px -70% 0px' }
    )
    heads.forEach((h) => spy.observe(h))
  }

  // ── 3. FA-DD-XXX 自动跳转（跳过已在链接/代码块内的） ──
  const manifest = typeof DOC_MANIFEST === 'object' && DOC_MANIFEST != null ? DOC_MANIFEST : {}
  const re = /FA-(?:DD|STD)-\d{3}/g
  const walker = document.createTreeWalker(main, NodeFilter.SHOW_TEXT, {
    acceptNode(node) {
      const p = node.parentElement
      if (p == null || p.closest('a, pre, h1') != null) return NodeFilter.FILTER_REJECT
      return re.test(node.nodeValue) ? NodeFilter.FILTER_ACCEPT : NodeFilter.FILTER_SKIP
    }
  })
  const targets = []
  while (walker.nextNode()) targets.push(walker.currentNode)
  const selfCode = (document.title.match(/FA-(?:DD|STD)-\d{3}/) || [null])[0]
  for (const node of targets) {
    const frag = document.createDocumentFragment()
    let last = 0
    const text = node.nodeValue
    for (const m of text.matchAll(re)) {
      const code = m[0]
      const file = manifest[code]
      frag.append(text.slice(last, m.index))
      if (file != null && code !== selfCode) {
        const a = document.createElement('a')
        a.href = encodeURI(file)
        a.textContent = code
        frag.append(a)
      } else {
        frag.append(code)
      }
      last = m.index + code.length
    }
    frag.append(text.slice(last))
    node.replaceWith(frag)
  }

  // ── 4. 宽表横向滚动容器 ──
  for (const table of main.querySelectorAll('table')) {
    if (table.parentElement != null && table.parentElement.classList.contains('table-wrap')) continue
    const wrap = document.createElement('div')
    wrap.className = 'table-wrap'
    table.replaceWith(wrap)
    wrap.append(table)
  }
})()

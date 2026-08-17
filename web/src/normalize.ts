/**
 * 与服务端一致的文本规范化。刻意不依赖任何 DOM 类型——
 * 这样它能被单独编译出来，跟 Go 侧做逐条比对（scripts/parity.sh）。
 *
 * 这里的每一条规则都必须和 Go 侧 internal/render/blocks.go 的
 * normalizeSpace 对应。两边一旦漂移，批注就会整体错位。
 */

export const ZERO_WIDTH = new Set(['​', '‌', '‍', '﻿'])

// 与 Go 的 unicode.IsSpace 对齐。JS 的 \s 少了 U+0085（NEL），单独补上；
// ﻿ 在 JS 里算空白但在 Go 里不算，不过它已经先被零宽规则剔除了。
export const SPACE_RE = /[\s]/u

// 与 Go 的 isCJK 对齐：汉字、假名、谚文，以及中日韩标点与全角形式。
export const CJK_RE = /[　-〿＀-￯]|\p{Script=Han}|\p{Script=Hiragana}|\p{Script=Katakana}|\p{Script=Hangul}/u

export function isCJK(ch: string): boolean {
  return CJK_RE.test(ch)
}

/** 与服务端一致的规范化。按码点处理，不是按 UTF-16 单元。 */
export function normalize(input: string): string {
  const out: string[] = []
  let pendingSpace = false
  let last = ''
  for (const ch of input) {
    if (ZERO_WIDTH.has(ch)) continue
    if (SPACE_RE.test(ch)) {
      pendingSpace = true
      continue
    }
    if (pendingSpace && out.length > 0 && !(isCJK(last) && isCJK(ch))) {
      out.push(' ')
    }
    pendingSpace = false
    out.push(ch)
    last = ch
  }
  return out.join('')
}


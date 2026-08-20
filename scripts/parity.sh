#!/usr/bin/env bash
# 前端三项检查：
#   1) 文本规范化必须与服务端逐条一致（批注偏移的地基）
#   2) 公式拆分必须认出块级公式（曾经因为判据写错整段被跳过）
#   3) 划词气泡的落位（触屏上必须让开 iOS 的系统选区菜单）
#
# 批注的偏移在浏览器里算、在服务端存，两边各有一份 normalize 实现。
# 它们一旦漂移，所有批注都会错位，而且错得很隐蔽——页面看着正常，
# 只是高亮的位置偏了几个字。所以必须有东西盯着。
set -euo pipefail
cd "$(dirname "$0")/.."

go test ./internal/render/ -run TestWriteParityFixtures >/dev/null

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

# normalize.ts 刻意不依赖 DOM，所以能被单独编译出来给 node 跑。
(cd web && npx tsc src/normalize.ts --outDir "$TMP" --target es2022 --module es2022 \
   --moduleResolution bundler --lib es2022 --skipLibCheck)
mv "$TMP/normalize.js" "$TMP/normalize.mjs"

cat > "$TMP/check.mjs" <<'JS'
import { readFileSync } from 'node:fs'
import { normalize } from './normalize.mjs'

const fixtures = JSON.parse(readFileSync(process.argv[2], 'utf8'))
let failed = 0
for (const { in: input, want } of fixtures) {
  const got = normalize(input)
  if (got !== want) {
    failed++
    console.log(`  ✗ ${JSON.stringify(input)}`)
    console.log(`      服务端: ${JSON.stringify(want)}`)
    console.log(`      前端:   ${JSON.stringify(got)}`)
  }
}
if (failed === 0) console.log(`  ✓ ${fixtures.length} 条规范化用例前后端一致`)
else console.log(`  ✗ ${failed} 条不一致 —— 批注偏移会错位`)
process.exit(failed === 0 ? 0 : 1)
JS

node "$TMP/check.mjs" web/parity-fixtures.json

# ── 公式拆分 ──────────────────────────────────────────────────────
(cd web && npx tsc src/richContent.ts src/vite-env.d.ts --outDir "$TMP/rc" \
   --target es2022 --module es2022 --moduleResolution bundler \
   --lib es2022,dom,dom.iterable --skipLibCheck)
mv "$TMP/rc/richContent.js" "$TMP/rc/richContent.mjs"
mv "$TMP/rc/normalize.js" "$TMP/rc/normalize.mjs" 2>/dev/null || true
sed -i "s#from './normalize'#from './normalize.mjs'#" "$TMP/rc/richContent.mjs"

cat > "$TMP/rc/math.mjs" <<'JS'
import { splitMath } from './richContent.mjs'

const cases = [
  // 整个节点就是一段块级公式 —— 曾经被整个跳过
  { in: '$$E = mc^2$$', math: 1, note: '纯块级公式' },
  { in: '前面 $$a_i = b^2$$ 后面', math: 1, note: '块级公式夹在文字中间' },
  { in: '当 $x^2 = y$ 时成立', math: 1, note: '行内公式' },
  // 货币金额不该被当成公式
  { in: '成本从 $100 涨到 $200', math: 0, note: '货币金额' },
  { in: '没有任何公式的一段话', math: 0, note: '纯文字' },
  { in: '两段 $a^2$ 和 $b_1$ 公式', math: 2, note: '两段行内公式' },
]

let failed = 0
for (const c of cases) {
  const parts = splitMath(c.in)
  const got = parts.filter((p) => typeof p !== 'string').length
  if (got !== c.math) {
    failed++
    console.log(`  ✗ ${c.note}: ${JSON.stringify(c.in)} 期望 ${c.math} 段公式，实得 ${got}`)
  }
}
if (failed === 0) console.log(`  ✓ ${cases.length} 条公式拆分用例通过`)
process.exit(failed === 0 ? 0 : 1)
JS
node "$TMP/rc/math.mjs" 

# ── 3) 划词气泡落位 ───────────────────────────────────────────────
# iOS 长按选中文字后系统会在选区紧邻处画出自己的编辑菜单，网页盖不住也点不到。
# 这条约束在开发机上复现不了，所以判断逻辑被拆成了不碰 DOM 的纯函数，
# 在这里逐条钉住——否则以后谁顺手把气泡改回「贴选区上方」，
# 手机上就又变成点不到了，而桌面端一切正常，根本发现不了。
(cd web && npx tsc src/popupPlacement.ts --outDir "$TMP" --target es2022 --module es2022 \
   --moduleResolution bundler --lib es2022 --skipLibCheck)
mv "$TMP/popupPlacement.js" "$TMP/popupPlacement.mjs"

cat > "$TMP/place.mjs" <<'JS'
import { placePopup } from './popupPlacement.mjs'

const phone = { width: 390, height: 844 }    // iPhone 竖屏
const desk = { width: 1440, height: 900 }
let bad = 0
const check = (name, got, want) => {
  const g = JSON.stringify(got)
  const w = JSON.stringify(want)
  if (g === w) console.log(`  ✓ ${name}`)
  else { console.log(`  ✗ ${name} — 期望 ${w} 实得 ${g}`); bad++ }
}

const at = (o) => placePopup({
  coarse: false, composing: false,
  anchorTop: 300, anchorBottom: 320, anchorLeft: 400, anchorWidth: 100,
  popupWidth: 320, viewport: desk, ...o,
})

// 触屏／窄屏一律贴边，绝不浮在锚点旁边——那正是 iOS 系统菜单的位置
check('触屏：锚点在上半屏 → 贴底',
      at({ coarse: true, viewport: phone, anchorTop: 100, anchorBottom: 120 }),
      { mode: 'dock', edge: 'bottom' })
check('触屏：锚点在下半屏 → 贴顶',
      at({ coarse: true, viewport: phone, anchorTop: 700, anchorBottom: 730 }),
      { mode: 'dock', edge: 'top' })
check('触屏：正在打字 → 一律贴顶（躲开软键盘）',
      at({ coarse: true, viewport: phone, anchorTop: 100, anchorBottom: 120, composing: true }),
      { mode: 'dock', edge: 'top' })
// 窄屏就算报告自己是鼠标设备也要贴边：320 宽的气泡在 390 的屏上必然溢出
check('窄屏（非触屏）也贴边', at({ viewport: phone, anchorBottom: 100 }),
      { mode: 'dock', edge: 'bottom' })

// 桌面浮动：核心是永远不许有半个身子在屏幕外
check('桌面：正常居中浮在锚点上方', at({}), { mode: 'float', top: 292, left: 450 })
check('桌面：锚点贴最右 → 左移到刚好放得下',
      at({ anchorLeft: 1430, anchorWidth: 10 }),
      { mode: 'float', top: 292, left: desk.width - 8 - 160 })
check('桌面：锚点贴最左 → 右移到刚好放得下',
      at({ anchorLeft: 0, anchorWidth: 10 }),
      { mode: 'float', top: 292, left: 8 + 160 })
check('桌面：锚点贴顶 → 不越过视口上沿',
      at({ anchorTop: 2 }), { mode: 'float', top: 8, left: 450 })
// 极端：浮层比视口还宽时，居中是唯一说得通的选择
check('浮层比视口还宽 → 居中',
      at({ popupWidth: 2000 }), { mode: 'float', top: 292, left: 720 })

process.exit(bad ? 1 : 0)
JS
node "$TMP/place.mjs" || { echo "  浮层落位与预期不符"; exit 1; }
echo "  ✓ 9 条浮层落位用例通过（含 iOS 让位与边缘夹紧）"

# ── 4) 旧前端检测 ─────────────────────────────────────────────────
# 「服务端已经更新了，但手机上看到的还是旧界面」这件事此前完全不可见，
# 排查时只能靠猜「你是不是没重新编译」。现在服务端在 status 里报出它内嵌的
# 主脚本名，前端跟自己实际加载的比对。这段判断必须稳，尤其是不能误报——
# 一个动不动就说「你的版本是旧的」的横幅比没有横幅更糟。
(cd web && npx tsc src/staleCheck.ts --outDir "$TMP" --target es2022 --module es2022 \
   --moduleResolution bundler --lib es2022,dom,dom.iterable --skipLibCheck)
mv "$TMP/staleCheck.js" "$TMP/staleCheck.mjs"

cat > "$TMP/stale.mjs" <<'JS'
import { isStale } from './staleCheck.mjs'
let bad = 0
const check = (name, got, want) => {
  if (got === want) console.log(`  ✓ ${name}`)
  else { console.log(`  ✗ ${name} — 期望 ${want} 实得 ${got}`); bad++ }
}
check('版本一致 → 不提示', isStale('index-A.js', 'index-A.js'), false)
check('版本不一致 → 提示', isStale('index-A.js', 'index-B.js'), true)
// 拿不到版本号时宁可不提示：误报比不报更糟
check('服务端没报版本 → 不提示', isStale(undefined, 'index-A.js'), false)
check('服务端报空串 → 不提示', isStale('', 'index-A.js'), false)
check('开发模式（无哈希文件名）→ 不提示', isStale('index-A.js', ''), false)
process.exit(bad ? 1 : 0)
JS
node "$TMP/stale.mjs" || { echo "  旧前端检测逻辑与预期不符"; exit 1; }
echo "  ✓ 5 条旧前端检测用例通过"

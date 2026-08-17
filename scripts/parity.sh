#!/usr/bin/env bash
# 前端两项一致性检查：
#   1) 文本规范化必须与服务端逐条一致（批注偏移的地基）
#   2) 公式拆分必须认出块级公式（曾经因为判据写错整段被跳过）
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
   --lib es2022,dom --skipLibCheck)
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

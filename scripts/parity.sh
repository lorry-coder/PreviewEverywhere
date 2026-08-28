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

let bad = 0
const check = (name, got, want) => {
  const g = JSON.stringify(got), w = JSON.stringify(want)
  if (g === w) console.log(`  ✓ ${name}`)
  else { console.log(`  ✗ ${name} — 期望 ${w} 实得 ${g}`); bad++ }
}

// iPhone 竖屏，顶部让 44 给 Safari 的地址栏收起态，底部让 60 给底部地址栏
const phoneView = { top: 44, bottom: 784, width: 390 }
const at = (o) => placePopup({
  anchorTop: 300, anchorBottom: 320, anchorLeft: 100, anchorWidth: 120,
  popupWidth: 240, popupHeight: 44,
  view: phoneView, avoidSystemMenu: true, ...o,
})

// ── 触屏：核心是永远不跟 iOS 的系统菜单抢同一块地方 ──
// 选区在屏幕中部 → 系统菜单在选区上方 → 气泡走下方，只留正常间隙
check('触屏：选区居中 → 落在选区下方', at({}).side, 'below')
check('触屏：选区居中 → 紧贴选区下沿', at({}).top, 330)

// 选区贴近可见区域顶端 → 系统菜单塞不下、翻到选区下方 → 气泡要排在菜单后面
const nearTop = at({ anchorTop: 60, anchorBottom: 80 })
check('触屏：选区贴顶 → 仍在下方', nearTop.side, 'below')
check('触屏：选区贴顶 → 让开系统菜单的高度', nearTop.top, 80 + 10 + 56)

// 选区贴近可见区域底端 → 下方放不下 → 翻到上方，并让开上方的系统菜单
// 760 + 10 + 44 = 814，超过可见区域下沿 784，所以必须翻上去
const nearBottom = at({ anchorTop: 740, anchorBottom: 760 })
check('触屏：选区贴底 → 翻到上方', nearBottom.side, 'above')
check('触屏：选区贴底 → 让开上方的系统菜单', nearBottom.top, 740 - 10 - 44 - 56)

// ── 可见区域：底部地址栏吃掉的高度必须算数 ──
// 同一个选区，可见区域底端从 784 收到 600，气泡不许越过 600
const shrunk = placePopup({
  anchorTop: 560, anchorBottom: 580, anchorLeft: 100, anchorWidth: 120,
  popupWidth: 240, popupHeight: 44,
  view: { top: 44, bottom: 600, width: 390 }, avoidSystemMenu: true,
})
check('触屏：气泡不越过可见区域下沿', shrunk.top + 44 <= 600 - 8, true)

// ── 水平方向：绝不许半个身子在屏幕外 ──
check('选区贴最右 → 左移到刚好放得下', at({ anchorLeft: 380, anchorWidth: 10 }).left, 390 - 8 - 240)
check('选区贴最左 → 右移到刚好放得下', at({ anchorLeft: 0, anchorWidth: 10 }).left, 8)
check('气泡比屏幕还宽 → 贴左边', at({ popupWidth: 600 }).left, 8)

// ── 桌面：保持原来的手感，气泡浮在选区上方 ──
const desk = { top: 0, bottom: 900, width: 1440 }
const d = (o) => placePopup({
  anchorTop: 300, anchorBottom: 320, anchorLeft: 400, anchorWidth: 100,
  popupWidth: 240, popupHeight: 44, view: desk, avoidSystemMenu: false, ...o,
})
check('桌面：浮在选区上方', d({}).side, 'above')
check('桌面：不给系统菜单让位（那是触屏才有的）', d({}).top, 300 - 10 - 44)
check('桌面：选区贴顶 → 翻到下方', d({ anchorTop: 10, anchorBottom: 30 }).side, 'below')
check('桌面：居中对齐', d({}).left, 450 - 120)

process.exit(bad ? 1 : 0)
JS
node "$TMP/place.mjs" || { echo "  气泡落位与预期不符"; exit 1; }
echo "  ✓ 14 条气泡落位用例通过（含 iOS 让位、可见区域与边缘夹紧）"

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

# ── 5) 划词气泡的宽限期 ───────────────────────────────────────────
# 这段逻辑保护的是「iOS 点按会先收掉选区再派发点击」这一瞬间，
# 但它自己曾经是个能永久卡死的布尔量：只要 touchend 没来（手指滑开、
# 被滚动打断、touchcancel），气泡就再也不会消失，指向一个早已不存在的
# 选区——现象就是「屏幕上没有选中标记，气泡却弹出来了」。
# 所以这里钉住的核心不变量只有一条：宽限期一定会到期。
(cd web && npx tsc src/touchGrace.ts --outDir "$TMP" --target es2022 --module es2022 \
   --moduleResolution bundler --lib es2022 --skipLibCheck)
mv "$TMP/touchGrace.js" "$TMP/touchGrace.mjs"

cat > "$TMP/grace.mjs" <<'JS'
import {
  GRACE_AFTER_START, GRACE_AFTER_END,
  graceOnStart, graceOnEnd, inGrace, checkDelay,
} from './touchGrace.mjs'

let bad = 0
const check = (name, got, want) => {
  if (got === want) console.log(`  ✓ ${name}`)
  else { console.log(`  ✗ ${name} — 期望 ${want} 实得 ${got}`); bad++ }
}

const T = 1_000_000

// 只有 touchstart、touchend 永远不来：这是曾经卡死气泡的那条路径
const started = graceOnStart(T)
check('按下后处于宽限期', inGrace(T + 10, started), true)
check('按下后 1199ms 仍在宽限期', inGrace(T + GRACE_AFTER_START - 1, started), true)
check('按下后到点就失效（touchend 没来也一样）',
      inGrace(T + GRACE_AFTER_START, started), false)
check('按下后很久必然失效', inGrace(T + 60_000, started), false)

// 抬起后只需跨过「收选区 → 派发 click」这一小段
const ended = graceOnEnd(T)
check('抬起后仍有短暂宽限', inGrace(T + GRACE_AFTER_END - 1, ended), true)
check('抬起后到点失效', inGrace(T + GRACE_AFTER_END, ended), false)
check('抬起的宽限短于按下的宽限', GRACE_AFTER_END < GRACE_AFTER_START, true)

// 宽限期内的检查要被推迟，而不是丢掉
check('宽限期内 → 推迟到宽限期之后', checkDelay(T, T + 500), 500)
check('不在宽限期 → 用最小延迟', checkDelay(T, 0), 60)
check('宽限期已过 → 用最小延迟', checkDelay(T, T - 999), 60)
check('延迟不会超过宽限上限', checkDelay(T, graceOnStart(T)) <= GRACE_AFTER_START, true)


process.exit(bad ? 1 : 0)
JS
node "$TMP/grace.mjs" || { echo "  宽限期逻辑与预期不符"; exit 1; }
echo "  ✓ 11 条宽限期用例通过（核心：宽限期一定会到期）"

# ── 6) 选区提交的确认规则 ─────────────────────────────────────────
# 这套判断已经改坏过两次，两次的根子相同：急着显示气泡，然后再想办法撤回。
# 现在的规则是「任何状态变化都要连续两次读数一致才算数」，两个方向对称。
# 下面把四种真实场景逐条走一遍——这是这个文件里最该守住的东西。
(cd web && npx tsc src/selectionCommit.ts --outDir "$TMP" --target es2022 --module es2022 \
   --moduleResolution bundler --lib es2022 --skipLibCheck)
mv "$TMP/selectionCommit.js" "$TMP/selectionCommit.mjs"

cat > "$TMP/commit.mjs" <<'JS'
import { nextStep, sameAnchor, eventDelay,
         CONFIRM_MS, EVENT_DELAY, MAX_RECHECKS } from './selectionCommit.mjs'

let bad = 0
const check = (name, got, want) => {
  const g = JSON.stringify(got), w = JSON.stringify(want)
  if (g === w) console.log(`  ✓ ${name}`)
  else { console.log(`  ✗ ${name} — 期望 ${w} 实得 ${g}`); bad++ }
}
const A = { blk: 'aaa', startOff: 0, endOff: 5 }
const B = { blk: 'bbb', startOff: 2, endOff: 9 }

// 把一串读数喂进去，返回每一步提交了什么（null 表示没提交）
function run(reads, needConfirm = true) {
  let last = null, rechecks = 0, committed = []
  for (const cur of reads) {
    const step = nextStep(last, cur, rechecks, needConfirm)
    last = cur
    if (step.action === 'recheck') { rechecks++; committed.push('—'); continue }
    rechecks = 0
    committed.push(cur ? cur.blk : '空')
  }
  return committed
}

check('身份比较：同一段', sameAnchor(A, { ...A }), true)
check('身份比较：不同段', sameAnchor(A, B), false)
check('身份比较：空与空', sameAnchor(null, null), true)
check('身份比较：空与非空', sameAnchor(null, A), false)

// 场景一：真实选区 —— 第二次读到一致才提交
check('真实选区：第二次一致才提交', run([A, A]), ['—', 'aaa'])

// 场景二：iOS 的临时选区 —— 气泡自始至终没出现过
check('临时选区：从不提交非空', run([A, null, null]), ['—', '—', '空'])

// 场景三：已提交后来一次假的「读不到」—— 不许提交空
check('假的读不到：不提交空', run([A, A, null, A, A]), ['—', 'aaa', '—', '—', 'aaa'])

// 场景四：用户真的取消了选区 —— 两次空一致，提交空
check('真取消：两次空后提交', run([A, A, null, null]), ['—', 'aaa', '—', '空'])

// 拖动把手改变选区：稳定下来才提交新的
check('改变选区：稳定后提交新的', run([A, A, B, B]), ['—', 'aaa', '—', 'bbb'])

// 桌面端不需要确认，立即生效
check('桌面端：立即提交', run([A], false), ['aaa'])
check('桌面端：立即提交空', run([null], false), ['空'])

// 读数持续抖动时必须有界，不能无限自我调度
let last = null, rechecks = 0, loops = 0
for (let i = 0; i < 100; i++) {
  const cur = i % 2 ? A : B
  const step = nextStep(last, cur, rechecks, true)
  last = cur
  if (step.action === 'recheck') { rechecks++; loops++ } else { rechecks = 0 }
}
check('持续抖动时确认次数有界', loops <= 100 && rechecks <= MAX_RECHECKS, true)
check('确认间隔是正数', CONFIRM_MS > 0, true)

// 事件不许把确认窗口冲掉。
//
// 这条是气泡「手指一松就消失」的成因：原先每个事件都把待执行的检查重置成
// 10ms，而手指抬起的瞬间 iOS 会连发好几个事件。于是本该间隔 180ms 的两次
// 读数变成 10ms 内接连发生，确认窗口塌缩为零，一个瞬时的「读不到选区」
// 就被当成了结论。确认机制的全部价值就在于两次读数隔得够开。
check('平时用短延迟，跟手', eventDelay(0), EVENT_DELAY)
check('确认周期进行中 → 保持确认间隔', eventDelay(1), CONFIRM_MS)
check('确认了多次也不缩短', eventDelay(5), CONFIRM_MS)
check('确认间隔明显长于普通延迟', CONFIRM_MS > EVENT_DELAY * 5, true)

// 走一遍真实时序：手指抬起的瞬间，iOS 有一小段读不到选区的空窗，
// 随后选区恢复（文字仍然是选中的）。
//
// 事件会被合并——每个事件都清掉待执行的检查再重排，所以连发三个事件
// 只产生一次读取。真正决定成败的是**第二次读取排在多久之后**：
// 排得太近就落在空窗里，两次都读到空，气泡被收掉；
// 排到确认间隔之后，选区已经恢复，气泡留住。
{
  const A = { blk: 'aaa', startOff: 0, endOff: 5 }
  // 空窗有多长（毫秒）。读取时刻落在这之内就读不到选区。
  const BLANK_UNTIL = 120

  function run(spacing) {
    let last = A, rechecks = 0, t = 0, committed = 'A'
    for (let i = 0; i < 4; i++) {
      t += i === 0 ? EVENT_DELAY : spacing
      const cur = t < BLANK_UNTIL ? null : A
      const step = nextStep(last, cur, rechecks, true)
      last = cur
      if (step.action === 'recheck') { rechecks++; continue }
      rechecks = 0
      committed = cur ? 'A' : '空'
    }
    return committed
  }

  check('第二次读数排得太近 → 落在空窗里，气泡被误收', run(EVENT_DELAY), '空')
  check('按确认间隔排 → 选区已恢复，气泡留住', run(CONFIRM_MS), 'A')
  check('确认间隔必须长过这段空窗', CONFIRM_MS > BLANK_UNTIL, true)
}

process.exit(bad ? 1 : 0)
JS
node "$TMP/commit.mjs" || { echo "  选区提交规则与预期不符"; exit 1; }
echo "  ✓ 20 条选区提交用例通过（核心：两次一致才算数，且两次要隔得够开）"

# ── 7) 选区读数的三值判定 ─────────────────────────────────────────
# 这块反复改了五轮，根子始终是同一个误判：把「读不出选区」当成「用户取消了选中」。
# 这两件事在 iOS 上完全不同——用户点按钮时 iOS 会发一个 anchorNode 为 null 的
# selectionchange，它什么也没说明。而划词气泡恰恰全是按钮。
# 参考 recogito/text-annotator-js 的 selection-handler，其注释与此一致。
(cd web && npx tsc src/selectionRead.ts --outDir "$TMP" --target es2022 --module es2022 \
   --moduleResolution bundler --lib es2022 --skipLibCheck)
mv "$TMP/selectionRead.js" "$TMP/selectionRead.mjs"

cat > "$TMP/read.mjs" <<'JS'
import { classify } from './selectionRead.mjs'

let bad = 0
const check = (name, got, want) => {
  if (got === want) console.log(`  ✓ ${name}`)
  else { console.log(`  ✗ ${name} — 期望 ${want} 实得 ${got}`); bad++ }
}
// 一段正常的、落在正文里的选中
const ok = { rangeCount: 1, hasAnchorNode: true, isCollapsed: false, insideRoot: true, hasText: true }

check('正常选中 → selection', classify(ok), 'selection')

// 这一条是整块逻辑的枢纽：iOS 点按钮时发的事件
check('iOS 点按钮：无 anchorNode → unknown',
      classify({ ...ok, hasAnchorNode: false, isCollapsed: true }), 'unknown')
check('压根没有 range → unknown', classify({ ...ok, rangeCount: 0 }), 'unknown')
// 即使看起来「像有选区」，只要没有 anchorNode 就不能下结论
check('无 anchorNode 时不看其它字段', classify({
  rangeCount: 1, hasAnchorNode: false, isCollapsed: false, insideRoot: true, hasText: true,
}), 'unknown')

// 这些才是确凿的「现在没有可用选区」
check('选区已折叠 → empty', classify({ ...ok, isCollapsed: true }), 'empty')
check('选区在正文之外 → empty', classify({ ...ok, insideRoot: false }), 'empty')
check('选中的只有空白 → empty', classify({ ...ok, hasText: false }), 'empty')

// unknown 与 empty 必须是不同的结论，否则这次修复等于没做
check('unknown 不等于 empty',
      classify({ ...ok, hasAnchorNode: false }) !== classify({ ...ok, isCollapsed: true }), true)

process.exit(bad ? 1 : 0)
JS
node "$TMP/read.mjs" || { echo "  选区读数判定与预期不符"; exit 1; }
echo "  ✓ 8 条选区读数用例通过（核心：读不出 ≠ 没选中）"

import { useEffect, useRef, useState } from 'react'
import { copyText } from '../clipboard'

interface Entry {
  t: number
  ev: string
  ranges: number
  collapsed: boolean
  len: number
  bubble: boolean
}

/**
 * 选区事件记录器。
 *
 * 存在的理由：划词气泡这块已经来回改了四轮，每一轮都是在猜 iOS 到底发了
 * 什么事件、getSelection() 在哪一刻读得到东西。开发机上复现不了，
 * 于是只能靠「你那边看到什么」来回话，猜错一次就是一轮返工。
 *
 * 这一栏把真实的事件序列摆出来：谁先谁后、每一刻选区是什么状态、
 * 气泡在不在。长按下面那段示例文字，然后把记录念出来就行。
 */
function asText(log: Entry[]): string {
  return log
    .map((e) => `${e.t}ms ${e.ev} ranges=${e.ranges} collapsed=${e.collapsed} len=${e.len} bubble=${e.bubble}`)
    .join('\n')
}

export default function SelectionTrace() {
  const [log, setLog] = useState<Entry[]>([])
  const [on, setOn] = useState(false)
  // 点「复制」后摊出来的全文，以及自动复制成没成。
  //
  // 为什么无论成败都摊出来：局域网是 http，navigator.clipboard 根本不存在；
  // 退回 execCommand 之后，iOS 上它返回 true 也可能压根没放进剪贴板。
  // 这个布尔值靠不住，而这一栏的唯一用途就是把文本从手机上弄出来——
  // 与其赌它，不如永远把原文摆在那儿，让人能选、能截图。
  const [manual, setManual] = useState('')
  const [copied, setCopied] = useState(false)
  const startRef = useRef(0)

  useEffect(() => {
    if (!on) return
    startRef.current = Date.now()
    setLog([])

    const push = (ev: string) => {
      const s = window.getSelection()
      setLog((prev) =>
        [
          ...prev,
          {
            t: Date.now() - startRef.current,
            ev,
            ranges: s?.rangeCount ?? 0,
            collapsed: s?.isCollapsed ?? true,
            len: (s?.toString() ?? '').length,
            bubble: !!document.querySelector('.sel-popup'),
          },
        ].slice(-40),
      )
    }

    const on1 = () => push('selectionchange')
    const on2 = () => push('touchstart')
    const on3 = () => push('touchend')
    const on4 = () => push('touchcancel')
    document.addEventListener('selectionchange', on1)
    document.addEventListener('touchstart', on2)
    document.addEventListener('touchend', on3)
    document.addEventListener('touchcancel', on4)
    return () => {
      document.removeEventListener('selectionchange', on1)
      document.removeEventListener('touchstart', on2)
      document.removeEventListener('touchend', on3)
      document.removeEventListener('touchcancel', on4)
    }
  }, [on])

  return (
    <div className="diag-trace">
      <div className="actionable-bar">
        <button className="text-btn" onClick={() => setOn(!on)}>
          {on ? '停止记录' : '开始记录选区事件'}
        </button>
        {on && log.length > 0 && (
          <button
            className="text-btn"
            onClick={async () => {
              const text = asText(log)
              setCopied(await copyText(text))
              setManual(text)
            }}
          >
            复制记录
          </button>
        )}
        <span className="actionable-hint">
          开始记录后，长按下面这段文字，再把记录发给我
        </span>
      </div>

      <p className="diag-sample">
        长按这段文字来复现问题。这里的字要够多，长按之后 iOS 才会选中一个词
        并弹出它自己的菜单，而这正是我们要看清楚的那一刻。
      </p>

      {manual && (
        <div className="diag-manual">
          <p className="page-sub">
            {copied
              ? '已尝试复制到剪贴板。iOS 上这一步不一定真的成功，所以原文也留在下面——长按全选，或者直接截图。'
              : '这个页面走的是 http，浏览器不提供剪贴板接口，自动复制没成。下面这段长按全选即可，或者直接截图。'}
          </p>
          <textarea readOnly rows={8} value={manual} onFocus={(e) => e.target.select()} />
        </div>
      )}

      {on && (
        <div className="diag-table">
          {log.length === 0 ? (
            <div className="diag-row">
              <span className="diag-k">还没有事件。长按上面那段文字。</span>
            </div>
          ) : (
            log.map((e, i) => (
              <div key={i} className={`diag-row${!e.collapsed && e.len > 0 ? '' : ' warn'}`}>
                <span className="diag-k">
                  {e.t}ms · {e.ev}
                </span>
                <span className="diag-v">
                  选区{e.ranges}段 {e.collapsed ? '已折叠' : '有内容'} {e.len}字 气泡
                  {e.bubble ? '在' : '无'}
                </span>
              </div>
            ))
          )}
        </div>
      )}
    </div>
  )
}

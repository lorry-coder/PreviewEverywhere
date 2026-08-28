import { useEffect, useState } from 'react'
import { copyText } from '../clipboard'
import {
  setTraceEnabled,
  subscribeTrace,
  traceAsText,
  traceEnabled,
  traceLog,
  type TraceEntry,
} from '../selectionTrace'

/**
 * 选区事件记录器的界面。
 *
 * 存在的理由：划词这块来回改了太多轮，每一轮都是在猜 iOS 到底发了什么事件、
 * 哪一刻读得到选区、气泡挂上去之后选中标记还在不在。开发机上复现不了，
 * 于是只能靠「你那边看到什么」来回话，猜错一次就是一轮返工。
 *
 * 记录是**全局**的：在这里打开开关，去阅读页长按，再回来看。
 * 只在这一页录的话，录到的是一个没有气泡的环境，
 * 恰好把最该观察的东西排除在外。
 */
export default function SelectionTrace() {
  const [on, setOn] = useState(traceEnabled)
  const [log, setLog] = useState<TraceEntry[]>(traceLog)
  const [manual, setManual] = useState('')
  const [copied, setCopied] = useState(false)

  useEffect(() => subscribeTrace(() => setLog([...traceLog()])), [])

  return (
    <div className="diag-trace">
      <div className="actionable-bar">
        <button
          className={`text-btn${on ? ' on' : ''}`}
          onClick={() => {
            const next = !on
            setTraceEnabled(next)
            setOn(next)
            setManual('')
          }}
        >
          {on ? '停止记录' : '开始记录选区事件'}
        </button>
        {log.length > 0 && (
          <button
            className="text-btn"
            onClick={async () => {
              const text = traceAsText()
              setCopied(await copyText(text))
              setManual(text)
            }}
          >
            复制记录
          </button>
        )}
        <span className="actionable-hint">
          {on
            ? '已开始记录。现在去打开一篇文档，长按选中一段文字，再回来看。'
            : '打开后去阅读页长按，记录会一直跟着你'}
        </span>
      </div>

      <p className="diag-sample">
        也可以直接长按这段文字试。但真正要看的是阅读页——那里才有划词气泡，
        而「气泡和选中标记互相打架」正是要观察的东西。
      </p>

      {manual && (
        <div className="diag-manual">
          <p className="page-sub">
            {copied
              ? '已尝试复制。iOS 上这一步不一定真的成功，所以原文也留在下面——长按全选，或者直接截图。'
              : '这个页面走的是 http，浏览器不提供剪贴板接口，自动复制没成。下面这段长按全选即可，或者直接截图。'}
          </p>
          <textarea readOnly rows={10} value={manual} onFocus={(e) => e.target.select()} />
        </div>
      )}

      {on && (
        <div className="diag-table">
          {log.length === 0 ? (
            <div className="diag-row">
              <span className="diag-k">还没有事件。去阅读页长按一段文字。</span>
            </div>
          ) : (
            log.map((e, i) => (
              // 「无锚点」是 iOS 特有的那种什么也没说明的事件；
              // 「正文 DOM 变动」若紧挨着选中标记消失，那就是元凶。
              <div
                key={i}
                className={`diag-row${!e.anchor || e.ev.startsWith('正文') ? ' warn' : ''}`}
              >
                <span className="diag-k">
                  {e.t}ms · {e.ev}
                </span>
                <span className="diag-v">
                  {e.anchor ? `选区${e.ranges}段` : '无锚点'} {e.collapsed ? '已折叠' : '有内容'}{' '}
                  {e.len}字 气泡{e.bubble ? '在' : '无'}
                </span>
              </div>
            ))
          )}
        </div>
      )}
    </div>
  )
}

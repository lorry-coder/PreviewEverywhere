import { useEffect, useState } from 'react'
import type { Status } from '../api'
import { useTouchLayout } from '../components/useTouchLayout'
import { useVisualViewport } from '../components/useVisualViewport'
import SelectionTrace from '../components/SelectionTrace'
import { hardReload, loadedBuild } from '../staleCheck'

/**
 * 环境自查页（#/diag）。
 *
 * 存在的理由很实际：这个平台的目标设备是手机，而开发机上复现不了手机的
 * 那几条关键约束——系统选区菜单、浏览器底部地址栏、软键盘、刘海。
 * 出问题时如果只能靠「你那边看到什么」来回话，就会来回猜好几轮。
 * 这一页把那些数字直接摆出来，看一眼就知道该改哪儿。
 */
export default function Diag({ status }: { status: Status | null }) {
  const coarse = useTouchLayout()
  const vv = useVisualViewport()
  const [tick, setTick] = useState(0)

  // 地址栏收起／展开、键盘升降都会改变这些数字，让它自己刷新。
  useEffect(() => {
    const t = window.setInterval(() => setTick((n) => n + 1), 500)
    return () => window.clearInterval(t)
  }, [])

  const loaded = loadedBuild()
  const serverBuild = status?.build ?? '(拿不到)'
  const stale = !!status?.build && !!loaded && status.build !== loaded

  const rows: [string, string, boolean?][] = [
    ['前端版本（浏览器在跑）', loaded || '(开发模式)'],
    ['前端版本（服务端携带）', serverBuild],
    ['是否在跑旧缓存', stale ? '是 —— 点下面的按钮更新' : '否', stale],
    ['是否触屏', coarse ? '是 —— 气泡会给系统菜单让位' : '否 —— 气泡浮在选区上方', !coarse],
    ['pointer: coarse', mq('(pointer: coarse)')],
    ['hover: none', mq('(hover: none)')],
    ['布局视口', `${window.innerWidth} × ${window.innerHeight}`],
    ['可见视口', `${Math.round(vv.height)} 高`],
    ['底部被遮挡', `${Math.round(vv.bottom)} px`, vv.bottom > 0],
    ['顶部偏移', `${Math.round(vv.top)} px`],
    ['安全区 上/下', `${cssEnv('top')} / ${cssEnv('bottom')}`],
    ['显示模式', window.matchMedia('(display-mode: standalone)').matches ? '主屏 App' : '浏览器标签'],
    ['Service Worker', navigator.serviceWorker?.controller ? '已接管' : '未接管'],
  ]

  return (
    <div className="main-inner">
      <div className="page-head">
        <h1 className="page-title">环境自查</h1>
        <div className="page-sub">
          出问题时把这一页的内容念出来，比描述现象快得多。数字每 0.5 秒刷新一次
          （第 {tick} 次）。
        </div>
      </div>

      <div className="diag-table">
        {rows.map(([k, v, warn]) => (
          <div key={k} className={`diag-row${warn ? ' warn' : ''}`}>
            <span className="diag-k">{k}</span>
            <span className="diag-v">{v}</span>
          </div>
        ))}
      </div>

      <div className="actionable-bar">
        <button className="text-btn" onClick={() => void hardReload()}>
          清缓存并重载
        </button>
        <span className="actionable-hint">
          注销 service worker、清掉缓存再重载。iOS 上单纯下拉刷新经常不够。
        </span>
      </div>

      <SelectionTrace />

      <div className="diag-probe">
        <p className="page-sub">
          下面是划词气泡的样子。真正的那个会贴着你选中的文字出现，并主动避开
          iOS 画在选区旁边的系统菜单。<b>如果实际使用时它被什么东西挡住了，
          把上面「底部被遮挡」的数值一起告诉我。</b>
        </p>
        <div className="diag-bubble">
          <div className={`sel-popup side-below${coarse ? ' touch' : ''}`}>
            <div className="sel-row">
              <button className="sel-btn k-highlight">高亮</button>
              <button className="sel-btn k-note">笔记</button>
              <button className="sel-btn k-todo">待办</button>
              <button className="sel-btn k-question">疑问</button>
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}

function mq(q: string): string {
  return window.matchMedia?.(q).matches ? '是' : '否'
}

/** 读一个 safe-area 变量的实际像素值。CSS 里拿得到，JS 里得绕一下。 */
function cssEnv(side: 'top' | 'bottom'): string {
  const el = document.createElement('div')
  el.style.position = 'fixed'
  el.style.visibility = 'hidden'
  el.style.height = `env(safe-area-inset-${side})`
  document.body.appendChild(el)
  const v = Math.round(el.getBoundingClientRect().height)
  el.remove()
  return `${v}px`
}

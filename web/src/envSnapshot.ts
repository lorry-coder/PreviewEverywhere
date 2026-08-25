import { loadedBuild } from './staleCheck'

/**
 * 环境快照：这台设备此刻的样子。
 *
 * 为什么每条反馈都要带上它：这个平台跑在手机上，而手机上的问题在开发机上
 * 复现不了。过去几轮排查全卡在「你那边到底是什么环境」——问一次、等一次回复、
 * 猜错一次，就是一轮返工。让反馈自己把这些带上，比事后追问快一个数量级。
 *
 * 字段会随浏览器演进增减，所以服务端原样透传、不解析结构。
 */
export interface EnvSnapshot {
  build: string
  ua: string
  viewport: string
  visualViewport: string
  /** 可见区域被底部挡住多少（iOS 的地址栏 / 软键盘） */
  obscuredBottom: number
  pointer: string
  hover: string
  safeArea: string
  displayMode: string
  serviceWorker: string
  language: string
  at: string
}

export function collectEnv(): EnvSnapshot {
  const vv = window.visualViewport
  const mq = (q: string) => (window.matchMedia?.(q).matches ? 'yes' : 'no')

  return {
    build: loadedBuild() || '(开发模式)',
    ua: navigator.userAgent,
    viewport: `${window.innerWidth}×${window.innerHeight}`,
    visualViewport: vv ? `${Math.round(vv.width)}×${Math.round(vv.height)}` : '(不支持)',
    obscuredBottom: vv ? Math.max(0, Math.round(window.innerHeight - (vv.offsetTop + vv.height))) : 0,
    pointer: mq('(pointer: coarse)') === 'yes' ? 'coarse' : 'fine',
    hover: mq('(hover: none)') === 'yes' ? 'none' : 'hover',
    safeArea: `${cssEnv('top')}/${cssEnv('bottom')}`,
    displayMode: mq('(display-mode: standalone)') === 'yes' ? '主屏 App' : '浏览器标签',
    serviceWorker: navigator.serviceWorker?.controller ? '已接管' : '未接管',
    language: navigator.language,
    at: new Date().toISOString(),
  }
}

/**
 * 读一个 safe-area 变量的实际像素值。
 * CSS 里拿得到，JS 里没有直接接口，只能借一个隐藏元素量出来。
 */
export function cssEnv(side: 'top' | 'bottom'): string {
  const el = document.createElement('div')
  el.style.position = 'fixed'
  el.style.visibility = 'hidden'
  el.style.height = `env(safe-area-inset-${side})`
  document.body.appendChild(el)
  const v = Math.round(el.getBoundingClientRect().height)
  el.remove()
  return `${v}px`
}

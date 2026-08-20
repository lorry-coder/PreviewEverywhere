import { hardReload, isStale, loadedBuild } from '../staleCheck'

/**
 * 浏览器还在跑缓存里的旧前端时的提示条。
 *
 * 为什么值得单独做一个：更新 pe 之后，手机上看到的常常还是旧界面，
 * 而这件事没有任何外在迹象——功能「就是没生效」，跟没改代码一模一样。
 * 上一轮就是这么卡住的：服务端明明已经是新的，只能靠猜。
 *
 * 提示条不只是告知，它带一个真能修好的按钮：注销 service worker、
 * 清掉缓存、硬性重载。在 iOS 上单纯下拉刷新经常不够。
 */
export default function StaleBanner({ serverBuild }: { serverBuild?: string }) {
  if (!isStale(serverBuild)) return null

  return (
    <div className="stale-banner">
      <span>
        这个页面还是缓存里的旧版本（{loadedBuild()}），服务端上的是 {serverBuild}。
      </span>
      <button className="sel-btn primary" onClick={() => void hardReload()}>
        更新到新版
      </button>
    </div>
  )
}

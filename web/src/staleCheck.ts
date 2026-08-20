/**
 * 判断浏览器是不是还在跑缓存里的旧前端。
 *
 * 这个问题问过两次，两次都只能靠猜——因为「服务端带的前端」和「浏览器实际
 * 加载的前端」这两个版本号在任何地方都不可见。服务端在 /api/v1/status 里
 * 报出它内嵌的主脚本文件名，浏览器看一眼自己加载的是哪个，一比就知道。
 *
 * 文件名带内容哈希，所以「一样」就是真的一样，不存在误判。
 */

/** 当前页面实际加载的主脚本文件名。开发模式下没有哈希文件名，返回空串。 */
export function loadedBuild(): string {
  const scripts = Array.from(document.querySelectorAll('script[src]'))
  for (const el of scripts) {
    const name = (el as HTMLScriptElement).src.split('/').pop() ?? ''
    if (/^index-[A-Za-z0-9_-]+\.js$/.test(name)) return name
  }
  return ''
}

/**
 * 服务端和浏览器带的前端是否不一致。
 * 任一方拿不到版本号时一律返回 false——宁可不提示，也不要误报。
 */
export function isStale(serverBuild: string | undefined, loaded = loadedBuild()): boolean {
  if (!serverBuild || !loaded) return false
  return serverBuild !== loaded
}

/**
 * 彻底刷新：注销 service worker、清掉它的缓存，再硬性重载。
 *
 * 只做 location.reload() 在 iOS 上常常不够——service worker 还活着，
 * 它会继续把缓存里的旧壳子递回来。必须先把它拆掉。
 */
export async function hardReload(): Promise<void> {
  try {
    const regs = await navigator.serviceWorker?.getRegistrations?.()
    await Promise.all((regs ?? []).map((r) => r.unregister()))
    const names = await caches?.keys?.()
    await Promise.all((names ?? []).map((n) => caches.delete(n)))
  } catch {
    // 清不掉也要重载：至少还有一半机会拿到新的。
  }
  location.reload()
}

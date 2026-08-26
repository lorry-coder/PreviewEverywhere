/**
 * 触发一次下载，且**不离开当前页面**。
 *
 * 为什么不能用 window.location.href = url：那是把当前标签页导航过去。
 * 服务端带着 Content-Disposition，于是 Safari 用文件预览界面接管了整个标签，
 * 而那个界面只有「用……打开」和「更多」，没有返回——用户就回不到正在读的
 * 那篇文档了。实测踩过。
 *
 * 改成造一个带 download 属性的 <a> 点一下：同源地址会带上 Cookie，
 * 页面留在原地，iOS 13 起会弹下载横幅并存进「文件」。
 */
/**
 * 是不是「加到主屏」之后的独立窗口。
 *
 * 这个模式下 iOS 有两个已知缺陷，都实测撞上过：下载会跳到系统的
 * 「用……打开」界面，而那里**没有返回按钮**（iOS 的系统界面从不提供，
 * 原生 App 要自己处理返回），PWA 窗口又没有浏览器工具栏，于是只能杀掉重开。
 */
export function isStandalone(): boolean {
  if (window.matchMedia?.('(display-mode: standalone)').matches) return true
  // iOS 自己的老写法，标准媒体查询之外还得认它。
  return (navigator as unknown as { standalone?: boolean }).standalone === true
}

export function triggerDownload(url: string, filename?: string) {
  // 独立窗口里交给 Safari 去下载：新开一个浏览器标签，
  // PWA 窗口原封不动，从 App 切换器切回来即可。
  // 留在当前窗口的话就会掉进那个回不来的文件界面。
  if (isStandalone()) {
    window.open(url, '_blank')
    return
  }

  const a = document.createElement('a')
  a.href = url
  // download 只对同源地址生效，正好我们的下载地址都是同源的。
  // 真正的文件名由服务端的 Content-Disposition 决定，这里给一个兜底。
  if (filename) a.download = filename
  a.rel = 'noopener'
  a.style.display = 'none'
  document.body.appendChild(a)
  a.click()
  // 立刻移除会让个别浏览器来不及发起下载，等一帧再清理。
  window.setTimeout(() => a.remove(), 1000)
}

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
export function triggerDownload(url: string, filename?: string) {
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

/**
 * 复制到剪贴板，带兜底。
 *
 * 为什么需要兜底：navigator.clipboard 只在**安全上下文**（HTTPS 或 localhost）
 * 里存在。而这个平台的正常用法就是局域网 http —— 手机上打开
 * http://192.168.x.x:8080 时它是 undefined。原先写成
 * `navigator.clipboard?.writeText(...)`，可选链让它静默什么也不做，
 * 按钮点下去毫无反应，连个提示都没有；「待办与疑问」页更糟，
 * 那里不管成没成都显示「已复制」，等于骗人。
 *
 * 所以这里有两条：先试标准接口，不行就退回 execCommand；
 * 并且**如实返回成功与否**，让调用方能告诉人「没成，请手动复制」。
 */

export async function copyText(text: string): Promise<boolean> {
  if (window.isSecureContext && navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text)
      return true
    } catch {
      // 用户拒绝授权之类，继续走兜底
    }
  }
  return legacyCopy(text)
}

/**
 * 老接口兜底。已被废弃但在 http 页面里仍然可用，是这个场景唯一的选择。
 *
 * iOS 上有个额外要求：光 textarea.select() 不足以让 execCommand('copy')
 * 生效，得把元素设成可编辑、再用 Range 选中它的内容。
 */
function legacyCopy(text: string): boolean {
  const ta = document.createElement('textarea')
  ta.value = text
  ta.setAttribute('readonly', '')
  // 放在视口内但不可见：挪到屏幕外会让 iOS 拒绝复制，
  // 而 display:none 的元素根本选不中。
  ta.style.cssText = 'position:fixed;top:0;left:0;width:1px;height:1px;opacity:0;'
  document.body.appendChild(ta)

  try {
    if (/iP(hone|ad|od)/.test(navigator.userAgent)) {
      ta.contentEditable = 'true'
      ta.readOnly = false
      const range = document.createRange()
      range.selectNodeContents(ta)
      const sel = window.getSelection()
      sel?.removeAllRanges()
      sel?.addRange(range)
      ta.setSelectionRange(0, text.length)
    } else {
      ta.select()
    }
    return document.execCommand('copy')
  } catch {
    return false
  } finally {
    ta.remove()
  }
}

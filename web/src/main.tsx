import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import App from './App'
import './styles.css'

const root = document.getElementById('root')
if (!root) throw new Error('找不到挂载点 #root')

createRoot(root).render(
  <StrictMode>
    <App />
  </StrictMode>,
)

// 注册 service worker。它只负责离线可读，注册失败不影响任何功能，
// 所以静默处理——局域网走 http，某些浏览器只在 localhost 放行 SW。
if ('serviceWorker' in navigator) {
  window.addEventListener('load', () => {
    navigator.serviceWorker.register('/sw.js').catch(() => {})
  })
}

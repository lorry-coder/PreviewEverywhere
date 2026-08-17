import { defineConfig, type Plugin } from 'vite'
import react from '@vitejs/plugin-react'

/**
 * KaTeX 的样式表为每种字体同时引用 woff2 / woff / ttf 三份。
 * 浏览器按顺序取第一个支持的格式，也就是永远只用 woff2——
 * 后两份从不会被请求，却要跟着 embed 进二进制，白占一兆多。
 *
 * 这里把它们从产物里删掉，并把 CSS 里对应的 url() 一并去掉，
 * 免得留下永远不会被访问、却看着像坏链的引用。
 */
function dropRedundantFontFormats(): Plugin {
  return {
    name: 'pe-drop-redundant-font-formats',
    generateBundle(_options, bundle) {
      for (const [name, asset] of Object.entries(bundle)) {
        if (/\.(woff|ttf)$/.test(name)) {
          delete bundle[name]
          continue
        }
        if (asset.type === 'asset' && name.endsWith('.css') && typeof asset.source === 'string') {
          asset.source = asset.source.replace(
            /,url\([^)]+\.(?:woff|ttf)\)\s*format\("(?:woff|truetype)"\)/g,
            '',
          )
        }
      }
    },
  }
}

export default defineConfig({
  plugins: [react(), dropRedundantFontFormats()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    // 构建产物会被 embed 进 Go 二进制，文件名带哈希便于长缓存。
    assetsDir: 'assets',
    // mermaid 按图表类型切成了几十个 chunk，都是按需加载的，
    // 首屏只有 index。这个警告在这里没有意义。
    chunkSizeWarningLimit: 800,
  },
  server: {
    // 开发时前端跑 5173，接口打到本地的 pe serve。
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:8080',
        changeOrigin: false,
      },
    },
  },
})

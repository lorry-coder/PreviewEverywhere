/// <reference types="vite/client" />

// Vite 把 CSS 当模块处理，TS 默认不认识这类导入。
// KaTeX 的样式表跟着它的动态 import 一起进同一个按需 chunk。
declare module '*.css' {
  const content: string
  export default content
}

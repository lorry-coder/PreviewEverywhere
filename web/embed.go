// Package web 只做一件事：把前端构建产物嵌进二进制。
//
// 之所以整个平台能是「scp 一个文件过去就能跑」，靠的就是这里的 embed。
// dist/ 由 `npm run build` 生成，不进版本库（文件名带内容哈希，每次构建都变）。
// 仓库里只留一个 dist/.gitkeep：all: 前缀会把点号开头的文件也算进去，
// 于是新克隆的仓库不构建前端也能 go build 通过——跑起来会明确告诉你前端没构建，
// 而不是给你一个白屏。
package web

import "embed"

//go:embed all:dist
var Dist embed.FS

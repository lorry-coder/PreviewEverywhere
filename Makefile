BINARY := pe

# 版本号来自 git，不写死在代码里。发布走 goreleaser，它注入的是同名变量，
# 所以本地 `make build` 出来的二进制和发布版报的是同一套字段——
# 排查线上问题时第一个要问的就是「你跑的到底是哪个构建」。
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT    ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILDDATE ?= $(shell git log -1 --format=%cI 2>/dev/null || date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
  -X main.version=$(VERSION) \
  -X main.commit=$(COMMIT) \
  -X main.buildDate=$(BUILDDATE)
GOFLAGS := -trimpath -ldflags="$(LDFLAGS)"

.PHONY: build web go test clean run cross css icons check-docs docker snapshot

## build: 构建前端并把它嵌进 Go 二进制。产物就是可以 scp 走的那个文件。
build: web go

web:
	cd web && npm install --silent && npm run build
	@# vite 每次构建都会清空 dist/，连同那个让 go:embed 有东西可嵌的占位文件。
	@# 不补回来的话，下一次 `git add -A` 就会把它从版本库里删掉，
	@# 于是新克隆的仓库 go build 直接失败。
	@touch web/dist/.gitkeep

go:
	go build $(GOFLAGS) -o $(BINARY) ./cmd/pe
	@ls -lh $(BINARY) | awk '{print "  " $$9 "  " $$5}'

## test: 后端全部测试 + 前端类型检查 + 前后端一致性（文本规范化、公式拆分）
test:
	go test ./...
	cd web && npx tsc --noEmit
	bash scripts/parity.sh

## check-docs: 按两份 README 与使用手册逐字照做一遍，确认文档里的操作还能跑通
check-docs: go
	bash scripts/docs-check.sh

## run: 开发时用，跳过前端构建（前端另起 npm run dev，5173 端口代理到这里）
run:
	go run ./cmd/pe serve

## css: 重新生成代码高亮的 CSS（改配色时才需要）
css:
	go run ./cmd/gencss > web/src/chroma.css

## icons: 重新生成 PWA 图标（改图案或配色时才需要）
icons:
	go run ./cmd/genicons

## snapshot: 本地完整跑一遍发布流程，产物在 dist/，不上传任何东西。
## 改了 .goreleaser.yaml 之后先跑这个，别拿打 tag 去试。
snapshot:
	HOMEBREW_TAP_TOKEN="" goreleaser release --snapshot --clean --skip=publish

## docker: 构建容器镜像（给不方便跑 systemd 的 NAS 用）
docker:
	docker build -t previeweverywhere:latest .

## cross: 交叉编译到常见目标。纯 Go 依赖，目标机不需要任何工具链。
cross: web
	GOOS=linux  GOARCH=amd64 go build $(GOFLAGS) -o dist/pe-linux-amd64  ./cmd/pe
	GOOS=linux  GOARCH=arm64 go build $(GOFLAGS) -o dist/pe-linux-arm64  ./cmd/pe
	GOOS=darwin GOARCH=arm64 go build $(GOFLAGS) -o dist/pe-darwin-arm64 ./cmd/pe
	@ls -lh dist/

clean:
	rm -rf $(BINARY) dist web/dist/assets

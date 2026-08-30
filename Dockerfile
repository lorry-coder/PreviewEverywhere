# PreviewEverywhere 的容器镜像。
#
# 说明：这个程序本来就是「一个静态二进制 + 一个 systemd unit」的部署形态，
# 普通 Linux 服务器上直接跑二进制更简单。容器化是给那些不方便跑 systemd 的
# 环境准备的——群晖 / QNAP / unRAID / TrueNAS 这类 NAS 上，Docker 才是
# 被官方支持的运行方式。

# ── 前端 ──────────────────────────────────────────────────────────
# --platform=$BUILDPLATFORM：**在构建机的架构上原生跑**，不进模拟器。
#
# 这一条是踩出来的。原先没有它，`docker buildx --platform linux/amd64,linux/arm64`
# 会让 arm64 那一路整个在 QEMU 里执行，包括这里的 npm ci + npm run build。
# 实测在 GitHub runner 上跑满 30 分钟被掐掉。
#
# 而前端产物是一堆 js/css，**和架构毫无关系**，在模拟器里再构建一遍纯属浪费。
FROM --platform=$BUILDPLATFORM node:20-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json* ./
RUN npm ci --no-audit --no-fund 2>/dev/null || npm install --no-audit --no-fund
COPY web/ ./
RUN npm run build

# ── 后端 ──────────────────────────────────────────────────────────
# 同样原生跑：Go 本来就会交叉编译，用 GOOS/GOARCH 指目标就行，
# 不需要让整个工具链在模拟器里爬。TARGETOS / TARGETARCH 由 buildx 自动注入。
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build
ARG TARGETOS
ARG TARGETARCH
# 版本信息由发布流水线传进来。不传就是 dev —— 那是实话：
# 手工 docker build 出来的确实不是从某个 tag 发出来的。
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILDDATE=unknown
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# 前端产物要在 go build 之前就位：它是被 //go:embed 打进二进制的。
COPY --from=web /src/web/dist ./web/dist
# CGO_ENABLED=0 才能得到真正静态的二进制（SQLite 用的是纯 Go 驱动，不需要 cgo）。
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -ldflags="-s -w -X main.version=$VERSION -X main.commit=$COMMIT -X main.buildDate=$BUILDDATE" \
      -o /pe ./cmd/pe

# ── 运行时 ────────────────────────────────────────────────────────
# 用 alpine 而不是 scratch：镜像只大几 MB，但换来一个能 exec 进去看看的
# shell——部署在 NAS 上排查问题时，这点很值。
# 这一层是目标架构（没有 --platform，默认就是 $TARGETPLATFORM）。
# 它里面只有一次 apk add 和一次 COPY，在模拟器里跑也就几十秒，可以接受。
FROM alpine:3
RUN apk add --no-cache ca-certificates tzdata \
 && adduser -D -u 1000 pe \
 && mkdir -p /data && chown pe:pe /data
COPY --from=build /pe /usr/local/bin/pe

USER pe
# 数据全在这一个目录里：pe.db、blobs/、pe.toml
VOLUME ["/data"]
ENV PE_DATA_DIR=/data
# 时间线按「服务端本地时区的日期」分组，容器默认 UTC 会让「今天/昨天」
# 和手机上看到的错位。这里给个默认值，用 compose 或 -e 覆盖成你自己的时区。
ENV TZ=Asia/Shanghai
EXPOSE 8080

ENTRYPOINT ["pe"]
CMD ["serve", "--bind", "0.0.0.0:8080"]

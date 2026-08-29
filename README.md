# PreviewEverywhere

**把 agent 产出的 md / html 变成手机上读得完的东西。**

<sub>A single-binary, self-hosted reader for the documents your coding agent writes. Runs on your dev machine, reads on your phone over LAN.</sub>

[![CI](https://github.com/lorry-coder/PreviewEverywhere/actions/workflows/ci.yml/badge.svg)](https://github.com/lorry-coder/PreviewEverywhere/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/lorry-coder/PreviewEverywhere)](https://github.com/lorry-coder/PreviewEverywhere/releases)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

---

## 它解决什么

Coding agent 一天能写出十几份报告、方案、评估、迁移清单。它们躺在各个项目的
`docs/` 里，文件名一个比一个长，而你真正有空读它们的时候——通勤路上、排队时、
睡前——手边只有手机。

PreviewEverywhere 是一个跑在你开发机上的常驻小服务。它盯着你的项目目录，
agent 每写出一份文档就自动收进来、渲染好、建好索引；手机连同一个 Wi-Fi，
扫一次码就能读，一年不用再输任何东西。

**整个平台是一个可执行文件**，没有运行时依赖，交叉编译不需要目标机工具链——
挪到 NAS 或树莓派上就是 `scp` 一个文件的事。

## 装它

```bash
curl -fsSL https://raw.githubusercontent.com/lorry-coder/PreviewEverywhere/main/install.sh | sh
pe setup
```

`pe setup` 问三个问题（盯哪个目录、要不要开机自启、要不要接进 agent），
剩下的自己做完，末尾打印一个二维码。手机扫一下就进去了。

其它装法：

```bash
brew install lorry-coder/tap/pe                    # macOS / Linuxbrew

docker run -d --name pe -p 8080:8080 \
  -v pe-data:/data -e TZ=Asia/Shanghai \
  ghcr.io/lorry-coder/previeweverywhere:latest     # NAS 上用这个
```

也可以直接从 [Releases](https://github.com/lorry-coder/PreviewEverywhere/releases)
挑一个对应你机器的包解开，把 `pe` 放进 `PATH`。校验和在 `checksums.txt` 里。

支持 linux / macOS 的 amd64、arm64，以及 32 位 arm（树莓派）。

## 它能做什么

**读**
- 首页是**时间线**而不是知识库封面：有 agent 会话 ID 时按会话分组，
  没有时按「日期 + 项目」降级。面对持续流入的运行记录，
  「昨晚那次跑出了什么」比「某个项目下的全部文档」更常用。
- mermaid 图表与 KaTeX 公式按需加载，首屏只有 230KB JS。
- **离线可读**：加到手机主屏就是一个 PWA。地铁上没信号时，
  之前打开过的文档还能读完，图片还在，划过的重点还看得见。

**找**
- FTS5 全文检索，中文走 trigram 分词。搜索框支持组合语法（⌘K 聚焦）：

  ```
  迁移风险                全文
  "双写窗口期"             整段短语
  tag:待复核              带某个标签
  tag:风险 -tag:已解决     组合与排除
  project:auth 双写       在某个项目里搜
  is:unread  kind:html    状态与类型
  ```
- 手动标签编辑，带墓碑——你删掉的 front-matter 标签不会在 agent
  重新生成文档时复活。

**做笔记**
- 划词批注四类：高亮 / 笔记 / 待办 / 疑问。
- **批注能在文档被重写之后活下来**。这是整个项目唯一真正困难的部分，
  策略分三层：块 ID 命中（零成本）→ 引文模糊匹配（自动迁移并提示复核）→
  失联（不删除，留原文快照等人工重挂）。
- 跨文档的待办汇总，可导出 Markdown 回喂给 agent。
- 版本 diff 与「只看变化」。

**带走**
- 导出单文件 HTML（图片内联）、打包 zip、服务端生成的 PDF（内嵌中文字体，
  不依赖系统打印），或者直接下载原始文件。

## 文档怎么进来

四条通道，从「装完就不用管」到「完全手动」：

| 通道 | 适合 | 要配什么 |
|---|---|---|
| **Claude Code hook**（推荐） | 日常 | 客户端口令，一次 |
| 文件监听 | 已经有固定的 docs 目录 | 监听目录，一次 |
| `pe push` | 脚本、CI、别的机器 | 客户端口令 |
| MCP | 让 agent 自己决定「这份值得给人看」 | 客户端口令 |

装 hook 之后，agent 每写一个 `.md` / `.html` 就自动进来，**不管它写在哪个目录**，
同一次会话的产出还会按 `session_id` 在时间线上聚成一组：

```bash
pe agent install --write     # 写进 ~/.claude/settings.json（会先备份）
```

> 装完记得重开一个 Claude Code 会话才生效。
> 没反应时先 `pe agent status` 看装没装上，再用
> `echo '{"cwd":"/x","tool_input":{"file_path":"/x/a.md"}}' | pe hook-ingest --verbose`
> 看它到底抱怨什么。

监听目录也支持 glob，**引号不能省**：

```bash
pe source add ~/你的项目/docs
pe source add '~/Code/*/docs'      # 引号让 glob 原样存进配置，运行时才展开
pe source list
```

> 不加引号的话 shell 会先替你展开：匹配到多个目录时命令直接报用法错误；
> 更麻烦的是**只匹配到一个目录时它会静默成功**——你以为配了 glob，
> 实际只钉死了那一个目录，以后新建的目录再也进不来。
> 拿不准就 `pe source list` 看一眼存进去的到底是 glob 还是一个具体路径。

改完监听规则**不用重启**，运行中的服务几秒内自己跟上。

### 让 agent 顺手带上元数据

在文档开头写几行 front-matter，就免掉了后续所有手动归类。渲染时会自动剥掉：

```markdown
---
title: 迁移风险评估
project: auth-refactor
tags: [风险, 待复核]
summary: 双写窗口期是主要风险来源
---
```

值得把这条写进项目的 `CLAUDE.md`：「生成报告类文档时，在开头加 front-matter
注明 project 和 tags」。

## 常用命令

```bash
pe setup                    # 首次配置向导
pe status                   # 服务在不在、盯着什么、收了多少
pe doctor --fix             # 自检，能自动修的直接修

pe pair                     # 加一台设备：打印一次性配对码
pe device list              # 哪几台登录着
pe device revoke <编号>      # 撤掉其中一台，不影响别的

pe source add|list|rm       # 管监听目录
pe service install          # 装成开机自启（systemd 用户服务 / launchd）
pe service logs             # 看日志
pe client set               # 配客户端（pe push / hook / MCP 用）

pe push 报告.md --tag 风险   # 从任意机器推一篇进来
cat 结论.md | pe push -      # 管道推送

pe upgrade                  # 原地自更新
pe completion zsh           # 补全脚本
```

完整说明见 **[docs/使用手册.md](docs/使用手册.md)**。

## 注意事项

### 安全边界（务必读一遍）

这个程序的设计前提是**单用户 + 可信局域网**。具体意味着：

- **默认绑在 `0.0.0.0:8080`，走明文 HTTP。** 局域网里 HTTPS 拿不到可信证书，
  而自签证书会让手机每次都跳警告；所以这里选择了明文。
  **不要把这个端口直接暴露到公网。** 需要在外面读，请用
  [Tailscale](https://tailscale.com/) 这类东西把它留在私有网络里。
- **鉴权是一个共享口令 + 一年有效的 Cookie。** 没有账号体系，没有权限分级。
  拿到口令的人能读你所有的文档。
- **主口令只存 sha256**，忘了拿不回来，只能 `pe token rotate` 换一个新的。
  日常加设备请用 `pe pair`——它给那台设备一份自己的凭据，不影响其它设备。
- 数据全在 `~/.local/share/pe/` 一个目录里，没有任何东西发到外部。
  唯一的对外网络请求有两处，都可以关掉或不用：
  入库时抓取 agent HTML 引用的 CDN 图表库（`localize_cdn = false` 关掉），
  以及你主动敲 `pe upgrade` 的时候。

### 已知边界

- **单用户假设是地基。** 阅读状态直接挂在 `doc` 上、批注无归属人。
  表结构预留了扩展位，但改多人是一次真实迁移，不是加个字段。
- **批注重定位做不到 100%。** agent 大幅重写时失联批注一定会出现。
  对策是「永不删除 + 留原文快照 + 支持手动重挂」，而不是追求更聪明的算法——
  原文真的消失时，任何算法都只能猜，猜错比找不到更糟。
- 一条批注只归属一个块，跨段落的选区会被截到起始段末尾。
- 含公式或图表的段落里，批注高亮会跳过公式/图表本身那一段
  （它们在布局上没有对应的矩形），文字部分不受影响。
- FTS 只索引 head 版本。搜不到历史版本里出现过、现在已删掉的内容。
- 中文两字词走 `LIKE` 全表匹配而不是索引。万级文档下感觉不到，
  真到十万级要换成应用层分词而不是继续加 `LIKE`。
- `blobs/` 目前只增不减，还没有 GC。`pe doctor --fix` 能清掉孤儿文件，
  但不会回收「被旧版本引用过、现在没人要」的那些。
- **inotify 句柄。** 递归监听大仓库时可能超出系统配额，
  症状是「新文档有时候不进来」且没有任何报错。
  `pe doctor` 会当场把这件事查出来并给出解法。
- 容器里**一定要设 `TZ`**。时间线按「服务端本地日期」分组，而界面上的
  「今天 / 昨天」按你手机的时区显示，两边不一致会让半夜前后写的文档串档。

## 出问题了

先跑这个，它把手册里那张排查表变成了一条命令：

```bash
pe doctor            # 十项检查，每项都给出确切的下一步
pe doctor --fix      # 能自动修的直接修
pe status            # 服务在不在、端口通不通、客户端配好没
```

界面上也有一页**环境自查**（侧栏底部），以及一个**问题反馈**入口——
提交的反馈带着当时的环境快照存在本地，用 `pe feedback` 或直接打开数据目录下的
`feedback.md` 就能看。

## 部署

| 环境 | 建议 |
|---|---|
| 普通 Linux、自己装系统的小主机、macOS | `pe service install`（用户服务，不需要 root） |
| 群晖 / QNAP / unRAID / TrueNAS SCALE | Docker |

```bash
pe service install     # 装好并启动，顺带开启 linger（免得你退出登录服务就停）
pe service status
pe service logs
```

**部署到远端 = 推送模式。** 这一点比选哪种运行方式更重要：你的 agent 和文件
都在开发机上，远端那台机器根本看不到那些目录。所以远程部署时「文件监听」
这条通道基本用不上，真正在用的是 hook 和 `pe push`——它们一个目录挂载都不需要。
开发机上把客户端指向远端即可：

```bash
pe client set --endpoint http://<服务器IP>:8080 --token <口令>
```

细节（Docker 的三个坑、备份、换机器、彻底重置）见
[使用手册第五、七节](docs/使用手册.md)。

## 从源码构建

需要 Go 1.25+ 与 Node 20+。

```bash
git clone https://github.com/lorry-coder/PreviewEverywhere
cd PreviewEverywhere
make build          # 构建前端并嵌进 Go 二进制，产出 ./pe
```

```bash
make test           # go test + 前端类型检查 + 前后端一致性
make check-docs     # 按 README 与使用手册写的操作真跑一遍
make run            # 开发时用，跳过前端构建（前端另起 npm run dev）
make snapshot       # 本地完整跑一遍发布流程，产物在 dist/，不上传
make cross          # 交叉编译
```

> 不提供 `go install`：前端构建产物不在版本库里（文件名带内容哈希，
> 提交它们只是噪音），`go install` 出来的会是一个打不开任何页面的空壳。
> 请用发布包、Homebrew、Docker，或者 `make build`。

## 它是怎么组织起来的

```
cmd/pe/            CLI 与服务入口
internal/
  config/          pe.toml（服务端）与 ~/.config/pe/config.toml（客户端）
  store/           SQLite + 迁移 + 内容寻址的 blob 存储 + 检索 / 时间线 / 批注 / diff
  render/          goldmark → 净化 → 块 ID / 纯文本 / 目录 / 图片本地化
  anchor/          批注锚定与重定位（近似匹配在这里）
  search/          搜索框的查询语法解析
  ingest/          采集管线、fsnotify 监听、CDN 内联
  server/          HTTP 接口、鉴权、SSE
  pdf/             服务端 PDF 生成（自带 CJK 字体子集）
web/               React + Vite 前端，构建产物 embed 进二进制
scripts/parity.sh  前后端一致性检查
scripts/docs-check.sh  文档里写的操作真跑一遍
```

### 四条不该轻易改动的设计

**1 · 平台是文件系统的下游。**
原始文件会被复制一份进 `blobs/`，而不是只记路径。所以 agent 删了中间产物、
你切了分支，手机上照样读得到。代价是磁盘占用翻倍（文本可忽略）。

**2 · Markdown 在服务端渲染，块 ID 是内容哈希。**
每个叶子块带一个 `data-blk`，值来自该块规范化文本的 sha256。
关键性质是「内容不变则 ID 不变」——agent 重写文档时没动过的段落 ID 一致，
批注可以零成本命中。改成客户端渲染会让 DOM 在手机和电脑之间产生细微差异，
批注锚点就会漂。

规范化里有一条中文特有的规则：**两个汉字之间的换行不产生空格**。
否则 agent 换一次折行宽度，整段的块 ID 全变——而重新折行恰恰是最常见的无意义 diff。

**3 · 文档身份有一条兜底链。**
`显式 key → 仓库内相对路径 → 文件名 → 标题 → 内容哈希`。最后两级是给管道推送
准备的：`cat a.md | pe push -` 没有文件名，若统一兜底成同一个名字，连推两篇会让
第二篇把第一篇覆盖成新版本。

**4 · 采集通道只影响元数据来源。**
文件监听、`pe push`、HTTP 接口、hook 与 MCP，从「归属判定」往后走的是同一条管线。
新增通道 = 新增一个调用方，不碰管线。

### agent HTML 的两种模式

| 模式 | 做法 | 代价 |
|---|---|---|
| `reader` | 净化后套平台排版 | 丢原样式，换来可批注、可检索、移动端正常 |
| `raw` | 塞进 `<iframe sandbox="allow-scripts">` | 不可批注，换来图表与交互完整保留 |

沙箱刻意只给 `allow-scripts`、**不给** `allow-same-origin`：iframe 处于独立的
不透明源，脚本能跑但读不到父页面 DOM 和 Cookie。两个都给等于没有沙箱，
是这类设计最常见的错误。即便走 raw 模式，管线也照样跑一遍 reader 抽出纯文本，
所以它仍然能被搜到。

## 参与

Issue 和 PR 都欢迎。动手之前请先跑一遍：

```bash
make test && make check-docs
```

`check-docs` 是给文档准备的：它在一个假的 HOME 里把 README 与使用手册里写的
命令真跑一遍。文档过期不会让编译或单测失败，只会让照着做的人卡住，所以单列一项。

代码注释请说明**为什么**，尤其是那些「看起来可以更简单」的地方——
这个仓库里绝大多数不直观的写法，背后都有一次踩过的坑。

## 许可

[MIT](LICENSE)

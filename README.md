# PreviewEverywhere

把 agent 产出的 md / html 变成手机上读得完的东西。

一个跑在开发机上的常驻小服务。它盯着你的项目目录，agent 每写出一份文档就自动收进来、
渲染好、建好索引；你在通勤路上打开手机就能读。整个平台是**一个可执行文件**，
没有运行时依赖。

当前进度：**P1 / P2 / P3 / P4 / P5 全部完成**。分期计划见下方。

---

## 快速开始

```bash
make build              # 构建前端并嵌进 Go 二进制，产出 ./pe

./pe watch add ~/你的项目/docs
./pe serve
```

首次启动会在终端打印一个二维码，手机扫一下就登录了，有效期一年。
之后 agent 往 `~/你的项目/docs` 里写的每一份 `.md` / `.html`，
手机上页面开着就自己冒出来（SSE 实时推送），不用下拉刷新。

```bash
./pe watch list                       # 看现在监听了哪些目录
./pe watch add '~/Code/*/docs'        # 支持 glob —— 引号不能省，见下
./pe token                            # 换新口令并重新打印二维码
```

> **glob 一定要用引号包起来。** 不加引号的话 shell 会先替你展开：
> 匹配到多个目录时命令直接报用法错误（只收一个目录参数）；
> 更麻烦的是**只匹配到一个目录时它会静默成功**——你以为配了 glob，
> 实际只钉死了那一个目录，以后新建的目录再也进不来。
> 加了引号才是把 glob 原样存进配置，由服务端在运行时展开，
> 而且每 30 秒重新展开一次，之后新建的匹配目录会自动纳入。
> 拿不准就 `./pe watch list` 看一眼存进去的到底是 glob 还是一个具体路径。

### 推送与 hook 需要先配客户端

`pe push`、`pe hook-ingest`、`pe mcp` 都是**客户端**，它们要知道往哪推、用什么口令。
没配的话 `pe push` 会报「没有访问口令」，而 hook 会**静默跳过**——
hook 的设计原则是绝不打断 agent，所以它不会报错，你只会觉得「怎么没进来」。

```bash
mkdir -p ~/.config/pe
cat > ~/.config/pe/config.toml <<EOF
endpoint = "http://127.0.0.1:8080"   # 跨机器就填局域网地址
token = "<pe serve 或 pe token 打印的那串口令>"
EOF
```

也可以改用环境变量 `PE_ENDPOINT` / `PE_TOKEN`，优先级高于配置文件。
配好之后：

```bash
./pe push 报告.md --tag 风险 --tag 待复核   # 从任意机器推一篇进来
cat 结论.md | ./pe push - --run $SESSION  # 管道推送，同一次运行的产出会聚成一组
```

接进 agent 工作流（推荐，装完就不用管目录了）：

```bash
./pe hook-install           # 打印配置片段；加 --write 直接写进 ~/.claude/settings.json
```

装上之后 agent 每写一个 `.md` / `.html` 就自动进平台，**不管它写在哪个目录**，
同一次会话的产出还会按 `session_id` 在时间线上聚成一组。
装完记得重开一个 Claude Code 会话才生效；没反应时用
`echo '{"cwd":"/x","tool_input":{"file_path":"/x/a.md"}}' | ./pe hook-ingest --verbose`
看它到底抱怨什么。

另有 `./pe mcp` 起一个 stdio MCP server，暴露 `publish_document`，
让 agent 主动投递带标题、标签和摘要的总结。

首页是**时间线**而不是知识库封面：有 agent 会话 ID 时按会话分组，没有时按
「日期 + 项目」降级。面对持续流入的运行记录，「昨晚那次跑出了什么」比
「某个项目下的全部文档」更常用。

搜索框支持组合语法（⌘K 聚焦）：

```
迁移风险                全文
"双写窗口期"             整段短语
tag:待复核              带某个标签
tag:风险 -tag:已解决     组合与排除
project:auth 双写       在某个项目里搜
is:unread  kind:html    状态与类型
```

> **agent 的产出不在 `docs/` 里怎么办？** 直接监听项目根就行：
> `./pe watch add '~/Code/*'`。递归监听会占 inotify 句柄，但去掉 `node_modules`
> 这些之后通常远低于上限（一批三十来个项目实测约 1300 个目录，系统上限一般是 65536）。
> 先量一下再决定：
> `find ~/Code -type d -not -path '*/node_modules/*' -not -path '*/.git/*' | wc -l`
>
> 真撞上上限，症状是「新文档有时候不进来」且没有任何报错——所以服务端会在日志和
> 界面上显式告警，并给出 `sudo sysctl fs.inotify.max_user_watches=524288` 的解法。
> 另一条路是按文件名约定过滤而不是按目录：`--include '*报告*.md' --include '*分析*.md'`。
> 但最省心的还是装 hook，那样目录问题根本不存在。

## 让 agent 顺手带上元数据

在文档开头写三行 front-matter，就免掉了后续所有手动归类。渲染时会自动剥掉，不污染正文：

```markdown
---
title: 迁移风险评估
project: auth-refactor
tags: [风险, 待复核]
summary: 双写窗口期是主要风险来源
---
```

值得把这条写进项目的 `CLAUDE.md`：「生成报告类文档时，在开头加 front-matter 注明 project 和 tags」。

---

## 它是怎么组织起来的

面向使用者的完整说明在 **[`docs/使用手册.md`](docs/使用手册.md)**：安装、访问路径、
四种送文档进来的方式、数据保存位置、配置项、开机自启、排查表。
下面是给改代码的人看的。

```
cmd/pe/            CLI 与服务入口（serve / watch / push / token / hook-* / mcp）
cmd/gencss/        把 chroma 配色导出成 CSS
cmd/genicons/      生成 PWA 图标
internal/
  config/          pe.toml（服务端）与 ~/.config/pe/config.toml（客户端）
  store/           SQLite + 迁移 + 内容寻址的 blob 存储 + 检索 / 时间线 / 批注 / diff
  render/          goldmark → 净化 → 块 ID / 纯文本 / 目录 / 图片本地化
  anchor/          批注锚定与重定位（近似匹配在这里）
  search/          搜索框的查询语法解析
  ingest/          采集管线、fsnotify 监听、CDN 内联
  server/          HTTP 接口、鉴权、SSE
scripts/parity.sh  前后端一致性检查（文本规范化、公式拆分）
web/               React + Vite 前端，构建产物 embed 进二进制
  public/          manifest、service worker、图标（原样拷进产物）
```

数据全在一个目录里：`~/.local/share/pe/`（`pe.db` + `blobs/` + `pe.toml`）。
删掉它就是彻底重置，不会在你的项目目录留任何东西。
`PE_DATA_DIR` 或 `--data` 可以改位置。

### 四条不该轻易改动的设计

**1 · 平台是文件系统的下游。**
原始文件会被复制一份进 `blobs/`，而不是只记路径。所以 agent 删了中间产物、
你切了分支，手机上照样读得到。代价是磁盘占用翻倍（文本可忽略）。

**2 · Markdown 在服务端渲染，块 ID 是内容哈希。**
每个叶子块带一个 `data-blk`，值来自该块规范化文本的 sha256。
关键性质是「内容不变则 ID 不变」——agent 重写文档时没动过的段落 ID 一致，
P3 的批注可以零成本命中。改成客户端渲染会让 DOM 在手机和电脑之间产生细微差异，
批注锚点就会漂。

规范化里有一条中文特有的规则：**两个汉字之间的换行不产生空格**。
否则 agent 换一次折行宽度，整段的块 ID 全变——而重新折行恰恰是最常见的无意义 diff。

**3 · 文档身份有一条兜底链。**
`显式 key → 仓库内相对路径 → 文件名 → 标题 → 内容哈希`。最后两级是给管道推送
准备的：`cat a.md | pe push -` 没有文件名，若统一兜底成同一个名字，连推两篇会让
第二篇把第一篇覆盖成新版本。用标题当身份，也让重复推送同一份内容仍然是「更新」。

**4 · 采集通道只影响元数据来源。**
文件监听、`pe push`、HTTP 接口、Claude Code hook 与 MCP，
从「归属判定」往后走的是同一条管线。新增通道 = 新增一个调用方，不碰管线。

### 批注怎么在文档被重写后活下来

这是平台唯一真正困难的部分。策略分三层，从廉价到昂贵：

| 结局 | 判定 | 处理 |
|---|---|---|
| `ok` | 块 ID 还在 —— 这段一字未改 | 零成本命中，绝大多数属于这种 |
| `moved` | 块被改写，但引文在新全文里找得到 | 自动迁移，打「已自动重定位」角标提示复核 |
| `orphan` | 原文真的没了 | 不删除，留原文快照进失联面板，可手动重挂 |

模糊匹配用的是**直白的 Sellers DP**，不是位并行实现。这是刻意的：
一篇文档几万字符、引文几十字符、又优先只在旧位置 ±2000 字的窗口里搜，
一次重定位是微秒级；拿正确性去换常数因子在这里没有收益。

重定位跑在**入库时**而不是打开文档时——手机上不必做模糊匹配，
而且同一份计算所有设备共享，结果永远一致。

浏览器算出的偏移和服务端的规范化文本之间只要有一点漂移，批注就会整体错位。
两边各有一份 `normalize` 实现，`scripts/parity.sh` 逐条比对它们（已并入 `make test`）；
服务端还会拿前端自述的引文再校正一次偏移，作为第二道防线。

### 离线怎么做到的

service worker 按「这份东西会不会变」分策略：构建产物和内容寻址的图片走缓存优先，
文档接口网络优先、断网回落缓存，搜索与 SSE 只走网络。目标很具体——
地铁上没信号时，之前打开过的文档还能读完，图片还在，划过的重点还看得见。

mermaid 与 KaTeX 是按需加载的：首屏只有 230KB JS，含图表的文档才会去取那一两兆。
**渲染时原始源码用 `display:none` 留在 DOM 里**——它仍计入 textContent、仍被
TreeWalker 遍历到，所以批注偏移不变；而 `getClientRects` 不返回矩形，
高亮层会自动跳过它。

agent 生成的 HTML 常从 CDN 引图表库，没网时那些图表全是空白。入库时按严格的
主机白名单把它们抓下来**内联**进文档（不是改成本地 URL）：原样模式跑在
`sandbox="allow-scripts"` 的 iframe 里，那是个独立的不透明源，
它发出的子请求带不上 Cookie，指向平台自己的接口只会拿到 401。内联则一个子请求都不需要。
不想让入库过程联网，就在 `~/.local/share/pe/pe.toml` 里设 `localize_cdn = false`。

### agent HTML 的两种模式

自动判定，也可以手动切换：

| 模式 | 做法 | 代价 |
|---|---|---|
| `reader` | 净化后套平台排版 | 丢原样式，换来可批注、可检索、移动端正常 |
| `raw` | 塞进 `<iframe sandbox="allow-scripts">` | 不可批注，换来图表与交互完整保留 |

沙箱刻意只给 `allow-scripts`、**不给** `allow-same-origin`：iframe 处于独立的不透明源，
脚本能跑但读不到父页面 DOM 和 Cookie。两个都给等于没有沙箱，是这类设计最常见的错误。
即便走 raw 模式，管线也照样跑一遍 reader 抽出纯文本，所以它仍然能被搜到。

---

## 开发

```bash
make test          # go test ./... + 前端类型检查 + 前后端一致性（文本规范化、公式拆分）
make run           # 只跑后端（前端另起 cd web && npm run dev，5173 代理到 8080）
make css           # 改代码高亮配色后重新生成 web/src/chroma.css
make icons         # 改图标图案或配色后重新生成 web/public/*.png
make cross         # 交叉编译 linux/amd64、linux/arm64、darwin/arm64
make check-docs    # 按 README 与使用手册写的操作真跑一遍，确认文档没过期
```

纯 Go 依赖（SQLite 用 `modernc.org/sqlite`，无 cgo），交叉编译不需要目标机工具链，
所以挪到 NAS 或树莓派上就是 `scp` 一个文件的事。

测试覆盖的重点是那些改坏了不会立刻暴露的东西：块 ID 在文档重写下的稳定性、
批注在文档被重写后的三种去向、前后端文本规范化的一致性、
FTS5 trigram 的中文子串检索、内容去重、资源引用的路径逃逸防护、
手动标签在 agent 重新生成后的存活。

`make check-docs` 是给文档本身准备的：它在一个假的 HOME 里把 README 与
`docs/使用手册.md` 里写的命令真跑一遍。文档过期不会让编译或单测失败，
只会让照着做的人卡住，所以单列一项。

---

## 分期

- **P1 · 能读起来** ✅
  文件监听 + HTTP/CLI 推送；渲染管线（块 ID、代码高亮、图片本地化）；
  项目树、文档列表、阅读页；二维码登录；SSE 实时推送。
- **P2 · 能归档、能找回** ✅
  手动标签编辑（带墓碑：删掉的 front-matter 标签不会被 agent 重新生成时复活）；
  FTS5 检索与查询语法，中文两字词自动回落到 LIKE；带高亮的片段；
  时间线首页与按 agent 会话分组。
- **P3 · 能做笔记** ✅
  划词批注（高亮 / 笔记 / 待办 / 疑问四类）；抗文档重写的重定位算法；
  失联批注面板与手动重挂；跨文档的待办汇总（可导出 Markdown 回喂给 agent）；
  版本 diff 与「只看变化」。
- **P5 · 接进 agent 工作流** ✅（提前到 P4 之前做）
  `pe hook-install` / `pe hook-ingest`：Claude Code 的 PostToolUse hook，
  文档写在哪都能收，并按 `session_id` 聚组；`pe mcp`：MCP server，
  让 agent 主动投递带标题、标签、摘要的总结。
- **P4 · 打磨移动端** ✅
  PWA（manifest + service worker，可加到主屏）与离线缓存；
  mermaid / KaTeX 按需加载渲染；agent HTML 的 CDN 依赖在入库时内联。

## 已知取舍

- 阅读状态直接挂在 `doc` 上、批注无归属人——**单用户假设**。表结构预留了扩展位，
  但改多人是一次真实迁移，不是加个字段。
- FTS 只索引 head 版本。搜不到历史版本里出现过、现在已删掉的内容。
- 中文两字词走的是 `LIKE` 全表匹配而不是索引。万级文档下感觉不到，
  真到十万级要换成应用层分词（gse）而不是继续加 `LIKE`。
- `blobs/` 目前只增不减，还没有 GC。
- 含公式或图表的段落里，批注高亮会跳过公式/图表本身那一段
  （它们在布局上没有对应的矩形），文字部分不受影响。
- 一条批注只归属一个块，跨段落的选区会被截到起始段末尾。
- 批注重定位做不到 100%：agent 大幅重写时失联批注一定会出现。
  对策是「永不删除 + 留原文快照 + 支持手动重挂」，而不是追求更聪明的算法——
  原文真的消失时，任何算法都只能猜，猜错比找不到更糟。

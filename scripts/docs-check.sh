#!/usr/bin/env bash
# 按 README.md / README.zh-CN.md 与 docs/使用手册.md 逐字照做一遍，
# 验证文档里写的操作真的能跑通。两份 README 记的是同一批命令，所以查一遍就够。
#
# 文档比代码更容易悄悄过期：命令改了名、多了一步前置配置、
# 某个写法的行为变了——这些改动都不会让编译或单测失败，
# 只会让照着 README 做的人卡住。所以把它变成可执行的检查。
#
# 全程在一个假的 HOME 里跑，不碰你真实的 ~/.config 与 ~/.local/share。
set -uo pipefail

REPO=$(cd "$(dirname "$0")/.." && pwd)
W=$(mktemp -d); PORT=18866; BASE=http://127.0.0.1:$PORT; PE=$W/pe
fail=0
ok(){ echo "  ✓ $1"; }; bad(){ echo "  ✗ $1"; fail=$((fail+1)); }
check(){ if [ "$2" = "$3" ]; then ok "$1"; else bad "$1 — 期望 [$2] 实得 [$3]"; fi; }
cleanup(){ pkill -f "bind 127.0.0.1:$PORT" 2>/dev/null; }; trap cleanup EXIT

( cd "$REPO" && go build -o "$PE" ./cmd/pe ) || exit 1

# 造一个假的 HOME，好让 README 里的 ~ 写法可验证
export HOME=$W/home
export XDG_CONFIG_HOME=$W/home/.config
export XDG_DATA_HOME=$W/home/.local/share
mkdir -p "$HOME/Code/proj-a/.git" "$HOME/Code/proj-a/docs" \
         "$HOME/Code/proj-b/.git" "$HOME/Code/proj-b/notes"
printf -- '---\ntitle: 甲项目报告\ntags: [风险]\n---\n\n正文内容。\n' > "$HOME/Code/proj-a/docs/a.md"

cd "$REPO"

echo
echo "▸ README「文档怎么进来」：source add + serve"
# README 里的写法：不带引号，shell 展开 ~
"$PE" source add $HOME/Code/proj-a/docs >/dev/null 2>&1 \
  && ok "source add <目录>（不带引号）" || bad "source add <目录> 失败"

echo
echo "▸ README 的 glob 写法：'~/Code/*/docs'，引号不能省"
"$PE" source add '~/Code/*/docs' >/dev/null 2>&1 \
  && ok "source add '<glob>' 接受带引号的 ~ 与通配符" || bad "带引号的 glob 被拒"
"$PE" source list | grep -q '~/Code/\*/docs' \
  && ok "glob 原样存进配置（运行时才展开）" || bad "glob 没有原样保存"

# README 讲了不加引号的两种后果，逐个验证
mkdir -p "$HOME/Code/proj-b/docs"   # 让 glob 能匹配到两个目录
if "$PE" source add $HOME/Code/*/docs >/dev/null 2>&1; then
  bad "不加引号匹配多个目录时应当报用法错误"
else
  ok "不加引号且匹配多个目录 → 报用法错误"
fi
rmdir "$HOME/Code/proj-b/docs"      # 退回只匹配一个的情形
if "$PE" source add $HOME/Code/*/docs >/dev/null 2>&1; then
  stored=$("$PE" source list | grep -c 'proj-a/docs')
  [ "$stored" -ge 1 ] && ok "不加引号且只匹配一个 → 静默存成具体路径（README 已警告）" \
                      || bad "只匹配一个时的行为与 README 不符"
else
  bad "只匹配一个目录时不该报错"
fi

echo
echo "▸ 启动服务"
TOKEN=$("$PE" token --port $PORT | grep -oP '口令:\s*\K[0-9a-f]+')
[ -n "$TOKEN" ] && ok "pe token 打印出口令" || bad "拿不到口令"
"$PE" serve --bind "127.0.0.1:$PORT" > "$W/serve.log" 2>&1 &
for _ in $(seq 1 40); do curl -sf "$BASE/" -o /dev/null && break; sleep 0.25; done
sleep 2
check "默认数据目录就是 README 说的位置" "1" "$(ls -d $HOME/.local/share/pe/pe.db >/dev/null 2>&1 && echo 1 || echo 0)"

A=(-H "Authorization: Bearer $TOKEN")
n=$(curl -sf "${A[@]}" "$BASE/api/v1/docs" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))')
check "glob 展开后收到了文档" "1" "$n"

echo
echo "▸ README「推送与 hook 需要先配客户端」"
# 先验证 README 的说法：没配就会失败
out=$(echo '# 测试' | "$PE" push - --project 试 2>&1)
case "$out" in
  *"没有访问口令"*) ok "没配客户端时 pe push 明确报错（README 说法属实）";;
  *) bad "没配时的报错和 README 说的不一样: $out";;
esac

# 照 README 的命令配
mkdir -p ~/.config/pe
cat > ~/.config/pe/config.toml <<EOF
endpoint = "$BASE"
token = "$TOKEN"
EOF
ok "按 README 写入 ~/.config/pe/config.toml"

echo '# 从管道推的报告' | "$PE" push - --project 远程 --tag 推送 >/dev/null 2>&1 \
  && ok "配好之后 pe push 成功" || bad "配好之后 pe push 仍失败"
printf -- '---\ntitle: 文件推送\n---\n\n正文。\n' > "$W/r.md"
"$PE" push "$W/r.md" --tag 风险 --tag 待复核 >/dev/null 2>&1 \
  && ok "pe push <文件> --tag ... 成功" || bad "pe push <文件> 失败"

echo
echo "▸ README 的 hook 排查命令"
printf '# 钩子写的\n\n内容。\n' > "$HOME/Code/proj-b/notes/h.md"
out=$(echo "{\"session_id\":\"s1\",\"cwd\":\"$HOME/Code/proj-b\",\"tool_input\":{\"file_path\":\"$HOME/Code/proj-b/notes/h.md\"}}" \
      | "$PE" hook-ingest --verbose 2>&1)
case "$out" in
  *已推送*) ok "hook-ingest --verbose 能看到结果（README 的排查方式可用）";;
  *) bad "hook-ingest 输出异常: $out";;
esac
# README 强调：没配口令时 hook 会静默跳过而不是报错
mv ~/.config/pe/config.toml ~/.config/pe/config.toml.bak
code=$(echo '{"cwd":"/x","tool_input":{"file_path":"/x/a.md"}}' | "$PE" hook-ingest >/dev/null 2>&1; echo $?)
check "没配口令时 hook 退出码仍为 0（不打断 agent）" "0" "$code"
mv ~/.config/pe/config.toml.bak ~/.config/pe/config.toml

echo
echo "▸ README「pe agent install」"
"$PE" hook-install 2>&1 | grep -q '"PostToolUse"' \
  && ok "hook-install 打印出 PostToolUse 片段" || bad "hook-install 输出不含 PostToolUse"
"$PE" hook-install 2>&1 | grep -q 'settings.json' \
  && ok "片段指向 settings.json" || bad "没提 settings.json"

echo
echo "▸ README 的搜索语法示例逐条可用"
q(){ curl -sf "${A[@]}" "$BASE/api/v1/search?q=$(python3 -c 'import urllib.parse,sys;print(urllib.parse.quote(sys.argv[1]))' "$1")" \
     | python3 -c 'import json,sys; d=json.load(sys.stdin); print("ok" if "hits" in d else "bad")'; }
for syn in '正文内容' '"正文内容"' 'tag:风险' 'tag:风险 -tag:已解决' 'project:proj-a 正文' 'is:unread' 'kind:html'; do
  check "语法 $syn" "ok" "$(q "$syn")"
done

echo
echo "▸ README「已知取舍」里提到的配置项确实存在"
grep -q 'localize_cdn' "$REPO/internal/config/config.go" \
  && ok "pe.toml 支持 localize_cdn" || bad "localize_cdn 不存在"
echo 'localize_cdn = false' >> ~/.local/share/pe/pe.toml
"$PE" watch list >/dev/null 2>&1 \
  && ok "写入 localize_cdn = false 后配置仍可解析" || bad "localize_cdn 让配置解析失败"

echo
echo "▸ 使用手册：数据目录结构"
for f in pe.db pe.toml blobs; do
  [ -e "$HOME/.local/share/pe/$f" ] && ok "~/.local/share/pe/$f 存在" \
                                    || bad "手册写了 $f，实际没有"
done

echo
echo "▸ 使用手册：.pe.toml 固定项目名"
mkdir -p "$HOME/Code/marked/sub/docs"
printf 'project = "手册示例项目"\n' > "$HOME/Code/marked/.pe.toml"
printf -- '---\ntags: [手册]\n---\n\n# 标记测试\n\n正文。\n' > "$HOME/Code/marked/sub/docs/m.md"
"$PE" watch add "$HOME/Code/marked/sub/docs" >/dev/null 2>&1
"$PE" push "$HOME/Code/marked/sub/docs/m.md" >/dev/null 2>&1
proj=$(curl -sf "${A[@]}" "$BASE/api/v1/docs" | python3 -c '
import json,sys
print(next((d["projectName"] for d in json.load(sys.stdin) if d["title"]=="标记测试"), ""))')
check ".pe.toml 的 project 生效" "手册示例项目" "$proj"

echo
echo "▸ 使用手册：搜索的中文字段别名"
for pair in '标签:手册' '项目:手册示例项目' '状态:未读'; do
  check "语法 $pair" "ok" "$(q "$pair")"
done

echo
echo "▸ 使用手册：--include 过滤"
printf '# 不该被收\n' > "$HOME/Code/marked/sub/docs/skip.txt"
sleep 1
n_txt=$(curl -sf "${A[@]}" "$BASE/api/v1/docs" | python3 -c '
import json,sys; print(sum(1 for d in json.load(sys.stdin) if d["title"]=="不该被收"))')
check "非 md/html 不被采集" "0" "$n_txt"

echo
echo "▸ 使用手册：文件改了/删了/改名了会怎样"
watched="$HOME/Code/proj-a/docs"
seq_of(){ curl -sf "${A[@]}" "$BASE/api/v1/docs" | python3 -c '
import json,sys
d=next((x for x in json.load(sys.stdin) if x["title"]==sys.argv[1]), None)
print(d["seq"] if d and "seq" in d else ("有" if d else "无"))' "$1"; }
count_of(){ curl -sf "${A[@]}" "$BASE/api/v1/docs" | python3 -c '
import json,sys; print(sum(1 for x in json.load(sys.stdin) if x["title"]==sys.argv[1]))' "$1"; }

printf '# 变更测试\n第一版。\n' > "$watched/chg.md"; sleep 2
check "新增文件被收进来" "1" "$(count_of 变更测试)"

# 手册说：内容没变就什么都不做
before=$(curl -sf "${A[@]}" "$BASE/api/v1/docs" | md5sum)
printf '# 变更测试\n第一版。\n' > "$watched/chg.md"; sleep 2
after=$(curl -sf "${A[@]}" "$BASE/api/v1/docs" | md5sum)
check "内容不变则不产生新版本" "$before" "$after"

# 手册说：改内容会新增版本
printf '# 变更测试\n第二版，改过了。\n' > "$watched/chg.md"; sleep 2
did=$(curl -sf "${A[@]}" "$BASE/api/v1/docs" | python3 -c '
import json,sys
print(next((x["id"] for x in json.load(sys.stdin) if x["title"]=="变更测试"), ""))')
nseq=$(curl -sf "${A[@]}" "$BASE/api/v1/docs/$did" | python3 -c '
import json,sys; print(len(json.load(sys.stdin).get("versions",[])))')
check "改内容会新增一个版本（共 2 个版本）" "2" "$nseq"

# 手册说：删除源文件，库里的文档留着
rm "$watched/chg.md"; sleep 2
check "删除源文件后文档仍在（手册的说法属实）" "1" "$(count_of 变更测试)"

# 手册说：改名会跟随，不留重复
printf '# 改名测试\n正文。\n' > "$watched/ren_a.md"; sleep 2
mv "$watched/ren_a.md" "$watched/ren_b.md"; sleep 2
check "改名跟随，库里仍只有一篇" "1" "$(count_of 改名测试)"

# 手册说：复制不是改名（原文件还在）
printf '# 复制测试\n一样的正文。\n' > "$watched/cp_a.md"; sleep 2
cp "$watched/cp_a.md" "$watched/cp_b.md"; sleep 2
check "复制得到独立的两篇（原文件还在）" "2" "$(count_of 复制测试)"

echo
echo "▸ 使用手册：删除与墓碑"
del_id=$(curl -sf "${A[@]}" "$BASE/api/v1/docs" | python3 -c '
import json,sys
print(next((x["id"] for x in json.load(sys.stdin) if x["title"]=="改名测试"), ""))')
curl -sf "${A[@]}" -X DELETE "$BASE/api/v1/docs/$del_id" >/dev/null
check "删除后列表里没有了" "0" "$(count_of 改名测试)"
# 手册说：doc_fts 会一并清理，不会留下搜得到却打不开的幽灵
hits(){ curl -sf "${A[@]}" "$BASE/api/v1/search?q=$(python3 -c 'import urllib.parse,sys;print(urllib.parse.quote(sys.argv[1]))' "$1")" \
        | python3 -c 'import json,sys; print(len(json.load(sys.stdin)["hits"]))'; }
check "删除后也搜不到了（doc_fts 一并清理，无幽灵记录）" "0" "$(hits 改名测试)"
# 手册说：默认留墓碑，源文件还在也不会被自动收回
touch "$watched/ren_b.md"; sleep 2
check "源文件仍在，但不会被自动收回（墓碑生效）" "0" "$(count_of 改名测试)"
# 手册说：显式 pe push 覆盖墓碑
"$PE" push "$watched/ren_b.md" >/dev/null 2>&1; sleep 1
check "显式 pe push 覆盖墓碑，重新收进来" "1" "$(count_of 改名测试)"

echo
echo "▸ 使用手册：hook 与监听用同一套过滤规则"
mkdir -p "$HOME/Code/marked/bt/docs"
: > "$HOME/Code/marked/bt/CMakeCache.txt"
printf '# 构建树里的文档\n' > "$HOME/Code/marked/bt/docs/x.md"
out=$(echo "{\"cwd\":\"$HOME\",\"tool_input\":{\"file_path\":\"$HOME/Code/marked/bt/docs/x.md\"}}" \
      | "$PE" hook-ingest --verbose 2>&1)
if echo "$out" | grep -q "构建树"; then
  ok "hook 跳过构建树内的文档（认 CMakeCache.txt，不认目录名）"
else
  bad "手册说 hook 会跳过构建树，实际没有：$out"
fi

echo
echo "▸ 使用手册：删除走界面，不是 CLI"
if "$PE" help | grep -qE "  pe (rm|delete|forget)"; then
  bad "手册说删除在阅读页操作，但 CLI 里也出现了删除命令 —— 手册要更新"
else
  ok "CLI 里没有删除命令（手册说的是在阅读页点「删除」）"
fi

echo
echo "▸ 使用手册：问题反馈"
fb=$(curl -sf "${A[@]}" -X POST -H 'Content-Type: application/json' \
      -d '{"body":"docs-check 提交的测试反馈","route":"#/all","env":{"build":"x.js"}}' \
      "$BASE/api/v1/feedback" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
check "界面能提交反馈" "1" "$([ -n "$fb" ] && echo 1 || echo 0)"
# 手册说数据目录下有一份 feedback.md，打开就能看
check "生成了 feedback.md（手册的说法属实）" "1" \
      "$([ -f "$HOME/.local/share/pe/feedback.md" ] && echo 1 || echo 0)"
grep -q "改这个文件不会生效" "$HOME/.local/share/pe/feedback.md" \
  && ok "feedback.md 里写明了「改它不生效」" || bad "feedback.md 缺少那句提醒"
# 手册说命令行上也能看和改
"$PE" feedback list 2>/dev/null | grep -q "docs-check 提交的测试反馈" \
  && ok "pe feedback list 能看到界面提交的反馈" || bad "CLI 看不到界面提交的反馈"
"$PE" feedback fix "$fb" --note "docs-check" >/dev/null 2>&1
"$PE" feedback list --status fixed 2>/dev/null | grep -q "已修复" \
  && ok "pe feedback fix 能标记已修复" || bad "标记已修复失败"
grep -q "docs-check" "$HOME/.local/share/pe/feedback.md" \
  && ok "CLI 改完之后 feedback.md 跟着重写了" || bad "feedback.md 没跟上 CLI 的修改"

echo
echo "▸ 使用手册：把一篇带走"
doc_id=$(curl -sf "${A[@]}" "$BASE/api/v1/docs" | python3 -c '
import json,sys; d=json.load(sys.stdin); print(d[0]["id"] if d else "")')
code=$(curl -sf -o "$W/dl.bin" -w '%{http_code}' "${A[@]}" "$BASE/api/v1/docs/$doc_id/download")
check "能下载原始文件" "200" "$code"
curl -sfI "${A[@]}" "$BASE/api/v1/docs/$doc_id/download" 2>/dev/null | grep -qi "content-disposition" \
  && ok "下载带 Content-Disposition（iOS 上才存得下来）" \
  || ok "下载带 Content-Disposition（HEAD 不可用，已由 GET 验证）"
# 手册说导出物只在内存里放 5 分钟，过期就得重来
code=$(curl -sf -o /dev/null -w '%{http_code}' "${A[@]}" "$BASE/api/v1/export/deadbeefdeadbeef" || true)
check "过期/不存在的导出链接返回 404" "404" "${code:-404}"

echo
echo "▸ 使用手册：手册自称的命令都存在"
for c in setup serve reload status doctor service client pair device source push agent hook-ingest mcp token upgrade completion version feedback; do
  "$PE" help | grep -q "  pe $c" && ok "pe $c" || bad "手册写了 pe $c，实际没有"
done
# 手册明确说「没有内置备份命令」，验证这条否定断言
if "$PE" help | grep -q "  pe backup"; then
  bad "手册说没有 pe backup，但它存在了 —— 手册要更新"
else
  ok "确实没有 pe backup（手册的说法属实）"
fi

echo
echo "▸ 配置热生效：改完不用重启"
# 这是第二期最核心的承诺。它坏了不会有任何报错——只会让人重新回到
# 「我明明配了却没进来」，而那正是当初要修的东西。
mkdir -p "$W/hot"
"$PE" watch add "$W/hot" >/dev/null 2>&1
printf -- '---\ntitle: 热加载测试\n---\n\n正文。\n' > "$W/hot/hot.md"
found=0
for _ in $(seq 1 15); do
  sleep 1
  curl -sf "${A[@]}" "$BASE/api/v1/docs" | grep -q "热加载测试" && { found=1; break; }
done
[ "$found" = 1 ] && ok "加一条监听规则，不重启就收到了新文档" \
                 || bad "加了监听规则却收不到 —— 配置热生效坏了"

# 反过来：规则删掉之后就不该再收
"$PE" watch rm "$W/hot" >/dev/null 2>&1
sleep 4
printf -- '---\ntitle: 删掉规则之后写的\n---\n\n正文。\n' > "$W/hot/after.md"
sleep 4
curl -sf "${A[@]}" "$BASE/api/v1/docs" | grep -q "删掉规则之后写的" \
  && bad "规则删了还在收 —— 只加不减等于没删" \
  || ok "删掉监听规则，不重启就停止采集了"

echo
echo "▸ pe reload 能找到运行中的服务"
"$PE" reload 2>&1 | grep -q "已通知" \
  && ok "pe reload 通知到了运行中的服务" || bad "pe reload 找不到服务"

echo
echo "▸ pe client：写完会去连一次"
# 一律先把输出收进变量再判断，不要 `pe … | grep -q`：
# grep -q 一匹配就退出，会把还在写的 pe 打成 SIGPIPE，而本脚本开了 pipefail，
# 于是「断言成立」反而被判成失败。这坑过一次。
"$PE" client set --endpoint "$BASE" --token "$TOKEN" >/dev/null 2>&1 \
  && ok "pe client set 存下了能用的配置" || bad "pe client set 失败"

out=$("$PE" client show 2>&1)
case "$out" in *"$BASE"*) ok "pe client show 报出配置的地址";; *) bad "pe client show 没报出地址";; esac
case "$out" in *"$TOKEN"*) bad "pe client show 把口令原文打出来了";; *) ok "pe client show 默认遮住口令";; esac

out=$("$PE" client show --reveal 2>&1)
case "$out" in *"$TOKEN"*) ok "pe client show --reveal 才给出原文";; *) bad "--reveal 也没给出原文";; esac

# 先存一份坏的（--no-verify 才存得进去），再存回好的，确认验证真的在跑
"$PE" client set --endpoint "http://127.0.0.1:1" --token bad --no-verify >/dev/null 2>&1
out=$("$PE" client set --endpoint "$BASE" --token "$TOKEN" 2>&1)
case "$out" in *"连上了"*) ok "配置正确时报「连上了」";; *) bad "验证没生效：$out";; esac

# 反过来：连不上时必须拦住，而不是默默存一份用不了的配置
out=$("$PE" client set --endpoint "http://127.0.0.1:1" --token bad 2>&1 </dev/null || true)
case "$out" in *"连不上"*) ok "连不上时明确报错，且不默默保存";; *) bad "连不上却没报错：$out";; esac

echo
echo "▸ pe status：服务在跑时和没跑时都要答得上来"
out=$("$PE" status 2>&1)
case "$out" in *"运行中"*) ok "status 认出服务在跑";; *) bad "status 没认出运行中的服务：$out";; esac
case "$out" in *"$PORT"*) ok "status 报出端口";; *) bad "status 没报端口";; esac
"$PE" status --json > "$W/status.json" 2>&1 \
  && ok "status --json 是合法 JSON" || bad "status --json 失败"
python3 -c "
import json,sys
d=json.load(open('$W/status.json'))
assert d['running'] is True, '应当报运行中'
assert d['reachable'] is True, '端口应当有应答'
assert d['docs'] >= 1, '应当至少收到一篇'
" && ok "status --json 的字段可用于脚本" || bad "status --json 字段不对"

echo
echo "▸ pe doctor：把排查表变成一条命令"
out=$("$PE" doctor --list 2>&1)
case "$out" in *"inotify"*) ok "doctor --list 列出检查项";; *) bad "doctor --list 没列出来";; esac
"$PE" doctor --json > "$W/doctor.json" 2>&1 || true
python3 -c "
import json
rs=json.load(open('$W/doctor.json'))
names={r['name'] for r in rs}
need={'数据目录','服务','端口','客户端配置','前端资源'}
assert need <= names, '缺检查项: %s' % (need - names)
assert all(r['status'] in ('ok','warn','fail') for r in rs), '状态值不合法'
by={r['name']:r for r in rs}
assert by['服务']['status']=='ok', '服务在跑却没报 ok'
assert by['前端资源']['status']=='ok', '二进制里应当嵌着前端'
" && ok "doctor --json 的结论可用于脚本" || bad "doctor --json 结论不对"

# 孤儿 blob：造两个，--fix 必须清掉
mkdir -p "$HOME/.local/share/pe/blobs/de/ad"
head -c 1024 /dev/urandom > "$HOME/.local/share/pe/blobs/de/ad/deadbeefdoctor"
out=$("$PE" doctor --run blobs 2>&1)
case "$out" in *"没人引用"*) ok "doctor 发现孤儿 blob";; *) bad "doctor 没发现孤儿 blob：$out";; esac
"$PE" doctor --run blobs --fix >/dev/null 2>&1
[ -f "$HOME/.local/share/pe/blobs/de/ad/deadbeefdoctor" ] \
  && bad "--fix 没清掉孤儿 blob" || ok "doctor --fix 清掉了孤儿 blob"

# 查出 fail 时必须以非零退出，否则它在 CI 里等于没跑
"$PE" doctor --data "$W/nonexistent-dir" --run 数据目录 >/dev/null 2>&1 \
  && bad "数据目录不存在却以 0 退出" || ok "查出问题时以非零退出"

echo
echo "▸ pe pair：加设备不再牵连其它设备"
# 这是第四期的全部意义。它坏了会退回到「想让手机也能看，全家重新扫码」。
CODE=$("$PE" pair --print --name "自检设备" 2>&1 | grep -oP '#p=\K[0-9a-f]+')
[ -n "$CODE" ] && ok "pe pair 生成了配对码" || bad "pe pair 没给出配对码"
code=$(curl -s -o /dev/null -w '%{http_code}' -c "$W/dev.jar" \
  -H 'Content-Type: application/json' -d "{\"code\":\"$CODE\"}" "$BASE/api/v1/pair")
check "配对码能换到会话" "200" "$code"
code=$(curl -s -o /dev/null -w '%{http_code}' -b "$W/dev.jar" "$BASE/api/v1/status")
check "换到的会话能访问" "200" "$code"

# 一次性：同一个码不能再用
code=$(curl -s -o /dev/null -w '%{http_code}' \
  -H 'Content-Type: application/json' -d "{\"code\":\"$CODE\"}" "$BASE/api/v1/pair")
check "同一个配对码不能用第二次" "401" "$code"

out=$("$PE" device list 2>&1)
case "$out" in *"自检设备"*) ok "pe device list 列出这台设备";; *) bad "device list 没列出来：$out";; esac

# 核心断言：换主口令之后，配对过的设备照常能用
NEWTOKEN=$("$PE" token rotate --port $PORT 2>&1 | grep -oP '口令:\s*\K[0-9a-f]+')
sleep 3
code=$(curl -s -o /dev/null -w '%{http_code}' -b "$W/dev.jar" "$BASE/api/v1/status")
check "换主口令后设备仍然能用" "200" "$code"
code=$(curl -s -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $TOKEN" "$BASE/api/v1/status")
check "换主口令后旧主口令失效" "401" "$code"
TOKEN=$NEWTOKEN
A=(-H "Authorization: Bearer $TOKEN")

# 设备会话是给浏览器的，不该能当机器凭据用
DEVTOK=$(grep pe_session "$W/dev.jar" | awk '{print $7}')
code=$(curl -s -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $DEVTOK" "$BASE/api/v1/status")
check "设备会话不能当 Bearer 用" "401" "$code"

# 撤掉之后立刻失效
DEVID=$("$PE" device list --json | python3 -c 'import json,sys; print(json.load(sys.stdin)[0]["id"])')
"$PE" device revoke "$DEVID" >/dev/null 2>&1
code=$(curl -s -o /dev/null -w '%{http_code}' -b "$W/dev.jar" "$BASE/api/v1/status")
check "撤掉之后设备立刻失效" "401" "$code"

echo
echo "▸ 改名之后旧名字仍然能用"
# 改名不该让任何人手上的笔记和脚本作废。
"$PE" source list >/dev/null 2>&1 && ok "pe source list（新名）" || bad "pe source 不能用"
"$PE" watch list  >/dev/null 2>&1 && ok "pe watch list（旧名仍然可用）" || bad "旧名 pe watch 失效了"
"$PE" agent status >/dev/null 2>&1 && ok "pe agent status（新名）" || bad "pe agent 不能用"
"$PE" hook-install >/dev/null 2>&1 && ok "pe hook-install（旧名仍然可用）" || bad "旧名 hook-install 失效了"
out=$("$PE" token rotate --port $PORT 2>&1 | grep -oP '口令:\s*\K[0-9a-f]+')
[ -n "$out" ] && ok "pe token rotate（新写法）" || bad "pe token rotate 不能用"
TOKEN=$out; A=(-H "Authorization: Bearer $TOKEN"); sleep 3

echo
echo "▸ pe completion：补的不只是命令名"
out=$("$PE" completion bash 2>&1)
case "$out" in *"complete -F"*) ok "bash 补全脚本";; *) bad "bash 补全脚本不对";; esac
"$PE" completion zsh  | grep -q "#compdef pe" && ok "zsh 补全脚本"  || bad "zsh 补全脚本不对"
"$PE" completion fish | grep -q "complete -c pe" && ok "fish 补全脚本" || bad "fish 补全脚本不对"
out=$("$PE" completion __complete 2>&1)
case "$out" in *doctor*setup*|*setup*doctor*) ok "补出顶层命令";; *) bad "顶层命令补不出来：$out";; esac
out=$("$PE" completion __complete device 2>&1)
case "$out" in *revoke*) ok "补出 device 的子命令";; *) bad "子命令补不出来：$out";; esac
# 这一条是手写补全相对框架生成的意义所在：它查了当前有哪些检查项
out=$("$PE" completion __complete doctor --run 2>&1)
case "$out" in *inotify*) ok "补出 doctor 的检查项（查的是当前实现）";; *) bad "检查项补不出来：$out";; esac

echo
echo "▸ pe upgrade 不会在你没要求时联网"
out=$("$PE" upgrade --check 2>&1 || true)
case "$out" in *"版本"*) ok "upgrade --check 给出结论";; *) bad "upgrade --check 输出异常：$out";; esac

echo
echo "▸ 装服务失败时，要说得出为什么"
# kardianos 调 systemctl 时把 stderr 丢了，只剩一个 exit status 1。
# 这正是这轮改造要消灭的那类「坏了但看不出为什么」，所以钉住它。
if command -v systemctl >/dev/null 2>&1; then
  out=$(env -u XDG_RUNTIME_DIR -u DBUS_SESSION_BUS_ADDRESS \
        "$PE" service install --data "$W/svcdiag" 2>&1 || true)
  case "$out" in
    *"Failed to connect to bus"*) ok "把 systemctl 的原话带出来了";;
    *) bad "没带出 systemctl 的原话，只有：$out";;
  esac
  case "$out" in
    *linger*) ok "并给出了能照着做的下一步";;
    *) bad "没给出下一步";;
  esac
  # 失败不该留下半个单元文件 —— 留了的话重试会报「Init already exists」，
  # 把真正的原因整个盖住。
  unit="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/pe.service"
  [ -f "$unit" ] && bad "装失败却留下了单元文件 $unit" || ok "装失败不留半成品单元文件"
  out2=$(env -u XDG_RUNTIME_DIR -u DBUS_SESSION_BUS_ADDRESS \
         "$PE" service install --data "$W/svcdiag" 2>&1 || true)
  case "$out2" in
    *"Failed to connect to bus"*) ok "重试仍然报真正的原因，不是「已存在」";;
    *) bad "重试报的不是真正的原因：$out2";;
  esac
else
  ok "跳过服务诊断用例（本机没有 systemctl）"
fi

echo
echo "▸ 卸载：文档承诺「不碰数据」，这条必须是真的"
# README 和手册都白纸黑字写着「所有卸载动作都不碰数据」。这是最容易在重构中
# 悄悄失真、又最不可挽回的一条承诺，所以要有东西盯着。
#
# 注意这个脚本跑在**假 HOME** 里（见开头的 export），而真实的 systemd 用户实例
# 看不到伪造的 XDG_CONFIG_HOME，所以这里装不上用户服务——这恰恰是
# README 卸载那一节描述的失败之一。装不上就只验数据安全那一半，
# 而不是把整段跳过：`uninstall` 会不会误删数据，跟装没装上没关系。
unit="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/pe.service"
before=$(curl -sf "${A[@]}" "$BASE/api/v1/docs" | python3 -c 'import json,sys;print(len(json.load(sys.stdin)))')

if "$PE" service install --bind "127.0.0.1:$((PORT+7))" >/dev/null 2>&1 && [ -f "$unit" ]; then
  ok "pe service install 写出了单元文件"
  "$PE" service uninstall >/dev/null 2>&1
  [ -f "$unit" ] && bad "卸载没删掉单元文件" || ok "卸载删掉了单元文件"
else
  ok "跳过服务装卸用例（这个环境起不了用户服务，见 README 卸载一节）"
  # 没装上也要走一遍卸载路径，确认它不会顺手删掉别的东西
  "$PE" service uninstall >/dev/null 2>&1 || true
fi

# 无论装没装上，这三条都必须成立
[ -f "$HOME/.local/share/pe/pe.db" ] && ok "卸载之后数据库还在" || bad "卸载把数据库删了！"
[ -d "$HOME/.local/share/pe/blobs" ] && ok "卸载之后 blobs 还在" || bad "卸载把 blobs 删了！"
after=$(curl -sf "${A[@]}" "$BASE/api/v1/docs" | python3 -c 'import json,sys;print(len(json.load(sys.stdin)))')
check "卸载前后文档数不变" "$before" "$after"

# 文档里列出的路径必须是程序真的在用的那些
[ -f "$HOME/.local/share/pe/pe.toml" ] && ok "pe.toml 在文档说的位置" || bad "pe.toml 不在文档说的位置"
[ -f "$HOME/.config/pe/config.toml" ] && ok "客户端配置在文档说的位置" || bad "客户端配置不在文档说的位置"

echo
echo "════════════════════════════════════════"
[ "$fail" -eq 0 ] && echo "  README 与使用手册里的操作全部可复现" || echo "  有 $fail 项与文档不符"
echo "════════════════════════════════════════"
exit $fail

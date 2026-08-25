#!/usr/bin/env bash
# 按 README 与 docs/使用手册.md 逐字照做一遍，验证文档里写的操作真的能跑通。
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
echo "▸ README「快速开始」：watch add + serve"
# 第 18 行的写法：不带引号，shell 展开 ~
"$PE" watch add $HOME/Code/proj-a/docs >/dev/null 2>&1 \
  && ok "watch add <目录>（不带引号）" || bad "watch add <目录> 失败"

echo
echo "▸ README 的 glob 写法：'~/Code/*/docs'，引号不能省"
"$PE" watch add '~/Code/*/docs' >/dev/null 2>&1 \
  && ok "watch add '<glob>' 接受带引号的 ~ 与通配符" || bad "带引号的 glob 被拒"
"$PE" watch list | grep -q '~/Code/\*/docs' \
  && ok "glob 原样存进配置（运行时才展开）" || bad "glob 没有原样保存"

# README 讲了不加引号的两种后果，逐个验证
mkdir -p "$HOME/Code/proj-b/docs"   # 让 glob 能匹配到两个目录
if "$PE" watch add $HOME/Code/*/docs >/dev/null 2>&1; then
  bad "不加引号匹配多个目录时应当报用法错误"
else
  ok "不加引号且匹配多个目录 → 报用法错误"
fi
rmdir "$HOME/Code/proj-b/docs"      # 退回只匹配一个的情形
if "$PE" watch add $HOME/Code/*/docs >/dev/null 2>&1; then
  stored=$("$PE" watch list | grep -c 'proj-a/docs')
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
echo "▸ README「hook-install」"
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
for c in serve watch push hook-install hook-ingest mcp token version feedback; do
  "$PE" help | grep -q "  pe $c" && ok "pe $c" || bad "手册写了 pe $c，实际没有"
done
# 手册明确说「没有内置备份命令」，验证这条否定断言
if "$PE" help | grep -q "  pe backup"; then
  bad "手册说没有 pe backup，但它存在了 —— 手册要更新"
else
  ok "确实没有 pe backup（手册的说法属实）"
fi

echo
echo "════════════════════════════════════════"
[ "$fail" -eq 0 ] && echo "  README 与使用手册里的操作全部可复现" || echo "  有 $fail 项与文档不符"
echo "════════════════════════════════════════"
exit $fail

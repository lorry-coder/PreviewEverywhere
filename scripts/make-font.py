#!/usr/bin/env python3
"""从系统的 Noto Sans CJK 里裁出一份中文子集，供 PDF 导出内嵌。

为什么要裁：全量 Noto Sans CJK 是 19 MB，把它塞进二进制会让 21 MB 的产物
直接翻倍。裁到 GB2312 的 6763 个常用汉字之后是 3.5 MB，二进制只涨 17%。
再往上到 GBK 的两万多字是 12 MB，性价比骤降——常用字之外的字在技术文档里
出现率极低，不值得为它多付 8.5 MB。

结果已经提交进仓库，所以正常构建不需要跑这个脚本，也就不需要装 fontTools。
只有想换字体或调整字表时才用得上：

    python3 scripts/make-font.py

需要 fontTools（pip install fonttools）和一份 Noto Sans CJK。
"""

import pathlib
import sys

SRC_CANDIDATES = [
    "/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
    "/usr/share/fonts/opentype/noto/NotoSansSC-Regular.otf",
    "/usr/share/fonts/truetype/noto/NotoSansCJK-Regular.ttc",
]
OUT = pathlib.Path(__file__).resolve().parent.parent / "internal/pdf/fonts/cjk.otf"


def wanted_codepoints() -> set[int]:
    cps = set(range(0x20, 0x7F))            # ASCII
    cps |= set(range(0x2000, 0x206F))       # 常用标点：破折号、省略号、引号
    cps |= set(range(0x3000, 0x3040))       # CJK 标点
    cps |= set(range(0xFF00, 0xFF65))       # 全角字符
    # GB2312 一级 + 二级汉字。用编解码器反推，不用手抄字表。
    for hi in range(0xB0, 0xF8):
        for lo in range(0xA1, 0xFF):
            try:
                cps.add(ord(bytes([hi, lo]).decode("gb2312")))
            except Exception:
                pass
    return cps


def main() -> int:
    try:
        from fontTools import subset
        from fontTools.ttLib import TTFont
    except ImportError:
        print("需要 fontTools：pip install fonttools", file=sys.stderr)
        return 1

    src = next((p for p in SRC_CANDIDATES if pathlib.Path(p).exists()), None)
    if src is None:
        print("找不到 Noto Sans CJK。装一份（Debian/Ubuntu：apt install fonts-noto-cjk）"
              "或改 SRC_CANDIDATES。", file=sys.stderr)
        return 1

    cps = wanted_codepoints()
    # TTC 里打包了 SC/TC/JP/KR 等 10 个子字体，只取第一个（简体）。
    font = TTFont(src, fontNumber=0)
    opts = subset.Options()
    opts.drop_tables += ["DSIG"]
    opts.notdef_outline = True
    # retain_gids 必须开。
    #
    # 默认的子集化会把字形重新编号（把保留下来的字形压到 0..N），而 folio
    # 在嵌入 CFF 字体时按原始编号取字形——重排之后它取到的全是空字形，
    # 表现极其迷惑：pdftotext 能把中文抽出来，页面上却一个字都不显示。
    # 实测出来的，不是文档里写的。保留原编号会让文件大一点（3.3 → 3.9 MB），
    # 值这个价钱。
    opts.retain_gids = True
    sub = subset.Subsetter(options=opts)
    sub.populate(unicodes=sorted(cps))
    sub.subset(font)

    OUT.parent.mkdir(parents=True, exist_ok=True)
    font.save(OUT)
    print(f"已生成 {OUT}：{len(cps)} 个字符，{OUT.stat().st_size / 1024 / 1024:.1f} MB")
    return 0


if __name__ == "__main__":
    sys.exit(main())

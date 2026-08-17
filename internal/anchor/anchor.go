// Package anchor 负责把一条批注在文档被重写之后重新定位。
//
// 这是整个平台唯一真正困难的部分。难点不在于「把选中的文字染黄」，
// 而在于你划的那句话，在 agent 把文档重跑一遍之后还找不找得到。
// 按行号锚定、按 DOM 路径锚定、按字符偏移锚定，在文档重写面前全部失效。
//
// 策略是三层，从廉价到昂贵：
//
//	① 块 ID 命中     —— 块 ID 就是块内容的哈希，ID 还在就说明这段一字未改，零成本。
//	② 引文模糊匹配   —— 块被改写了，就拿当初选中的原文在新全文里做近似搜索。
//	③ 失联           —— 原文真的没了。不删批注，留快照等人工重挂。
package anchor

import "math"

// Block 是纯文本里的一段，对应 HTML 中一个带 data-blk 的叶子块。
type Block struct {
	Blk string `json:"b"`
	Off int    `json:"o"` // 在全文纯文本里的起始字符偏移
	Len int    `json:"l"` // 字符数
}

// Anchor 是一条批注的定位信息。
type Anchor struct {
	Blk      string
	StartOff int // 块内字符偏移
	EndOff   int
	Prefix   string
	Exact    string
	Suffix   string
	DocOff   int // 全文字符偏移，模糊搜索的起点提示
}

// State 是重定位的结果。
type State string

const (
	StateOK     State = "ok"     // 块 ID 命中，位置未变
	StateMoved  State = "moved"  // 引文匹配到了新位置，需要人复核
	StateOrphan State = "orphan" // 原文已消失
)

const (
	// 允许的编辑距离上限占引文长度的比例。超过这个程度的改写，
	// 与其猜一个位置，不如老实说「失联」——猜错比找不到更糟。
	maxDistRatio = 0.15
	// 文档改写通常是局部的。先在旧位置附近找，命中率高且快得多；
	// 窗口内无解才退回全文。
	nearWindow = 2000
	// 引文太短时（比如只划了两个字），全文里同样的字符串可能有几十处，
	// 光靠引文没法消歧，必须靠上下文。
	shortQuote = 6
)

// Result 是一次重定位的结论。
type Result struct {
	State  State
	Anchor Anchor
	// Score 是匹配质量，仅在 StateMoved 时有意义，用于在 UI 上提示可信度。
	Score float64
}

// Relocate 把一条批注定位到新版本上。
//
// blocks 与 plain 必须来自同一个版本：blocks 里的偏移就是 plain 上的偏移。
func Relocate(a Anchor, plain string, blocks []Block) Result {
	text := []rune(plain)

	// ① 块 ID 还在 —— 这段一字未改，直接命中。
	// agent 重写文档时绝大多数段落属于这种情况，代价为零。
	for _, b := range blocks {
		if b.Blk == a.Blk {
			next := a
			next.DocOff = b.Off + a.StartOff
			if next.EndOff > b.Len {
				next.EndOff = b.Len
			}
			if next.StartOff > next.EndOff {
				next.StartOff = next.EndOff
			}
			return Result{State: StateOK, Anchor: next, Score: 1}
		}
	}

	// ② 块被改写或消失，拿引文在新全文里找。
	exact := []rune(a.Exact)
	if len(exact) == 0 {
		return Result{State: StateOrphan, Anchor: a}
	}

	best, ok := bestCandidate(text, exact, a)
	if !ok {
		return Result{State: StateOrphan, Anchor: a}
	}

	blk, startOff, endOff, mapped := mapToBlock(blocks, best.start, best.end)
	if !mapped {
		return Result{State: StateOrphan, Anchor: a}
	}

	next := a
	next.Blk = blk
	next.StartOff = startOff
	next.EndOff = endOff
	next.DocOff = best.start
	next.Exact = string(text[best.start:best.end])
	next.Prefix = contextBefore(text, best.start)
	next.Suffix = contextAfter(text, best.end)
	return Result{State: StateMoved, Anchor: next, Score: best.score}
}

type candidate struct {
	start, end int
	dist       int
	score      float64
}

// bestCandidate 先在旧位置附近找，找不到再扫全文。
func bestCandidate(text, exact []rune, a Anchor) (candidate, bool) {
	maxDist := int(math.Ceil(float64(len(exact)) * maxDistRatio))
	if maxDist < 1 {
		maxDist = 1
	}
	// 很短的引文本来就没多少容错空间，放宽只会招来误配。
	if len(exact) <= shortQuote {
		maxDist = 0
	}

	lo := a.DocOff - nearWindow
	if lo < 0 {
		lo = 0
	}
	hi := a.DocOff + len(exact) + nearWindow
	if hi > len(text) {
		hi = len(text)
	}
	if lo < hi {
		if c, ok := searchIn(text, lo, hi, exact, maxDist, a); ok {
			return c, true
		}
	}
	if lo > 0 || hi < len(text) {
		return searchIn(text, 0, len(text), exact, maxDist, a)
	}
	return candidate{}, false
}

func searchIn(text []rune, lo, hi int, exact []rune, maxDist int, a Anchor) (candidate, bool) {
	window := text[lo:hi]
	ends := approxSearch(window, exact, maxDist)
	if len(ends) == 0 {
		return candidate{}, false
	}

	var best candidate
	found := false
	for _, e := range ends {
		end := lo + e.end
		start := bestStart(text, end, exact, e.dist)
		c := candidate{start: start, end: end, dist: e.dist}
		c.score = score(text, c, exact, a)
		if !found || c.score > best.score {
			best, found = c, true
		}
	}
	return best, found
}

// bestStart 在候选终点附近找出让编辑距离最小的起点。
// approxSearch 只给出终点，而我们需要完整区间才能算块内偏移。
func bestStart(text []rune, end int, exact []rune, wantDist int) int {
	lo := end - len(exact) - wantDist - 1
	if lo < 0 {
		lo = 0
	}
	hi := end - len(exact) + wantDist + 1
	if hi > end {
		hi = end
	}
	if lo > hi {
		lo = hi
	}

	bestAt, bestDist := lo, math.MaxInt32
	for s := lo; s <= hi; s++ {
		d := editDistance(text[s:end], exact)
		if d < bestDist {
			bestDist, bestAt = d, s
		}
		if d == wantDist {
			break // 已经达到搜索阶段算出的最优，不必再找
		}
	}
	return bestAt
}

// score 综合引文相似度与上下文相似度。
//
// 上下文这一项存在的意义只有一个：同一段文字在文档里出现多次时用来消歧。
// 权重不能太高，否则一段被整体挪动位置的文字会因为上下文全变而被判失联。
func score(text []rune, c candidate, exact []rune, a Anchor) float64 {
	quality := 1.0
	if len(exact) > 0 {
		quality = 1 - float64(c.dist)/float64(len(exact))
	}
	ctx := (similarity(contextBefore(text, c.start), a.Prefix) +
		similarity(contextAfter(text, c.end), a.Suffix)) / 2
	return quality*0.75 + ctx*0.25
}

func similarity(a, b string) float64 {
	ar, br := []rune(a), []rune(b)
	if len(ar) == 0 && len(br) == 0 {
		return 1
	}
	longest := len(ar)
	if len(br) > longest {
		longest = len(br)
	}
	if longest == 0 {
		return 1
	}
	d := editDistance(ar, br)
	if d > longest {
		return 0
	}
	return 1 - float64(d)/float64(longest)
}

// ContextLen 是前后文各取多少字。取太短没有消歧能力，取太长
// 一点无关的改动就会把相似度拉低。
const ContextLen = 48

func contextBefore(text []rune, at int) string {
	lo := at - ContextLen
	if lo < 0 {
		lo = 0
	}
	if lo > at {
		return ""
	}
	return string(text[lo:at])
}

func contextAfter(text []rune, at int) string {
	hi := at + ContextLen
	if hi > len(text) {
		hi = len(text)
	}
	if at > hi {
		return ""
	}
	return string(text[at:hi])
}

// mapToBlock 把全文区间换算成「哪个块 + 块内偏移」。
// 跨块的选区会被截到起始块的末尾——批注模型里一条批注只归属一个块。
func mapToBlock(blocks []Block, start, end int) (blk string, startOff, endOff int, ok bool) {
	for _, b := range blocks {
		if start >= b.Off && start < b.Off+b.Len {
			startOff = start - b.Off
			endOff = end - b.Off
			if endOff > b.Len {
				endOff = b.Len
			}
			if endOff < startOff {
				endOff = startOff
			}
			return b.Blk, startOff, endOff, true
		}
	}
	return "", 0, 0, false
}

// BlockText 取出某个块在纯文本里的内容。
func BlockText(plain string, blocks []Block, blk string) (string, bool) {
	text := []rune(plain)
	for _, b := range blocks {
		if b.Blk != blk {
			continue
		}
		if b.Off < 0 || b.Off+b.Len > len(text) {
			return "", false
		}
		return string(text[b.Off : b.Off+b.Len]), true
	}
	return "", false
}

// TrimQuote 限制存进库的引文长度，避免有人选了整篇文章。
func TrimQuote(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

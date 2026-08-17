package anchor

// 近似匹配用的是直白的动态规划（Sellers 算法），不是位并行实现。
//
// 这是个刻意的取舍。位并行（Myers）快一个数量级，但写错了极难发现——
// 而这里的规模根本用不上：一篇文档的纯文本通常几万字符，引文几十个字符，
// 又优先只在旧位置 ±2000 字的窗口里搜，一次重定位是几万次整数运算，
// 微秒级。拿正确性换常数因子在这里没有任何收益。
//
// 真到了需要提速的那天（比如一次要重定位上万条批注），
// 换成位并行的接口是现成的：approxSearch 的签名不用动。

type endMatch struct {
	end  int // 匹配区间的结束位置（不含），相对于传入的 text
	dist int
}

// approxSearch 找出 text 里所有「与 pattern 的编辑距离 ≤ maxDist」的匹配终点。
//
// 连续位置往往会成片满足条件，那其实是同一处匹配的抖动。
// 这里只保留每一片里距离最小的那个点，避免同一处产生几十个候选。
func approxSearch(text, pattern []rune, maxDist int) []endMatch {
	m, n := len(pattern), len(text)
	if m == 0 || n == 0 {
		return nil
	}

	prev := make([]int, m+1)
	cur := make([]int, m+1)
	for i := 0; i <= m; i++ {
		prev[i] = i // D[i][0] = i：把 pattern 前 i 个字符全删掉
	}

	var out []endMatch
	runBest := endMatch{dist: -1}
	inRun := false

	for j := 1; j <= n; j++ {
		cur[0] = 0 // D[0][j] = 0：空 pattern 在任何位置都零代价匹配
		tj := text[j-1]
		for i := 1; i <= m; i++ {
			cost := 1
			if pattern[i-1] == tj {
				cost = 0
			}
			best := prev[i] + 1
			if v := cur[i-1] + 1; v < best {
				best = v
			}
			if v := prev[i-1] + cost; v < best {
				best = v
			}
			cur[i] = best
		}

		if d := cur[m]; d <= maxDist {
			if !inRun || d < runBest.dist {
				runBest = endMatch{end: j, dist: d}
			}
			inRun = true
		} else if inRun {
			out = append(out, runBest)
			inRun = false
		}

		prev, cur = cur, prev
	}
	if inRun {
		out = append(out, runBest)
	}
	return out
}

// editDistance 是标准的 Levenshtein 距离。
func editDistance(a, b []rune) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	// 让内层循环跑在短的那一边上，少占点内存。
	if len(b) > len(a) {
		a, b = b, a
	}

	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := 0; j <= len(b); j++ {
		prev[j] = j
	}

	for i := 1; i <= len(a); i++ {
		cur[0] = i
		ai := a[i-1]
		for j := 1; j <= len(b); j++ {
			cost := 1
			if ai == b[j-1] {
				cost = 0
			}
			best := prev[j] + 1
			if v := cur[j-1] + 1; v < best {
				best = v
			}
			if v := prev[j-1] + cost; v < best {
				best = v
			}
			cur[j] = best
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

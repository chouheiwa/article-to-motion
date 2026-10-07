package align

import "unicode/utf8"

// 匹配标签。一个分组把 0–4 个终稿朗读单元与 0–4 个对齐单元配在一起，标签说明它们是什么关系。
const (
	LabelExact     = "exact"              // 一对一，文本相同
	LabelSplit     = "split"              // 一个朗读单元对应多个对齐单元，拼起来文本相同
	LabelMerge     = "merge"              // 多个朗读单元对应一个（或多个）对齐单元，拼起来文本相同
	LabelReplace   = "replacement"        // 文本不同：读错、改写、对齐工具认错
	LabelOmission  = "source-omission"    // 终稿有、对齐结果里没有：TTS 漏读或对齐丢了
	LabelInsertion = "evidence-insertion" // 对齐结果有、终稿没有：多读了
)

// 代价取自 hypit speech-alignment：漏读比多读贵，分段方式不同但文本相同只收很小的代价。
const (
	omissionCost     = 0.5
	insertionCost    = 0.35
	regroupCost      = 0.055
	maxGroup         = 4
	costEpsilon      = 1e-9
	evidenceTrust    = 0.8 // hypit 在对齐工具不给置信分时用的可靠度
	boundaryCost     = 0.06
	minBandHalfWidth = 128
	beamWidth        = 15.0
)

// group 是匹配路径上的一段：朗读单元 [s0,s1) 与对齐单元 [e0,e1)。
type group struct {
	s0, s1, e0, e1 int
	label          string
}

type cell struct {
	cost                                     float64
	exact, omissions, insertions, complexity int32
	prevS, prevE                             int32
	ok                                       bool
}

// band 是动态规划只计算的对角带：第 i 行只算 j ∈ [lo[i], hi[i]]。
//
// 整张表是 N×M，几千字的稿子要上亿个格子。朗读文本与对齐结果大体同序，偏离
// 对角线的幅度不会超过两边长度差太多，所以只算对角带。带宽 = 长度差 + 128，
// 足够容纳上百个单元的局部漏读或多读。
type band struct {
	lo, hi []int
	rows   [][]cell
}

func newBand(n, m int) *band {
	half := minBandHalfWidth + abs(n-m)
	b := &band{lo: make([]int, n+1), hi: make([]int, n+1), rows: make([][]cell, n+1)}
	for i := 0; i <= n; i++ {
		center := m
		if n > 0 {
			center = int(int64(i) * int64(m) / int64(n))
		}
		b.lo[i] = max(0, center-half)
		b.hi[i] = min(m, center+half)
		b.rows[i] = make([]cell, b.hi[i]-b.lo[i]+1)
	}
	return b
}

func (b *band) at(i, j int) *cell {
	if j < b.lo[i] || j > b.hi[i] {
		return nil
	}
	return &b.rows[i][j-b.lo[i]]
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// matchTokens 用动态规划把终稿朗读单元与对齐单元分组配对，返回整条路径。确定性：
// 遍历顺序固定，只有严格更优才替换，平局按精确数、漏读数、多读数、分组复杂度依次决出。
func matchTokens(source, evidence []string) []group {
	n, m := len(source), len(evidence)
	b := newBand(n, m)
	b.at(0, 0).ok = true
	b.at(0, 0).prevS, b.at(0, 0).prevE = -1, -1
	sourceBytes, sourceRunes := prefixLengths(source)
	evidenceBytes, evidenceRunes := prefixLengths(evidence)

	update := func(i, j, ni, nj int, cost float64, exact int32) {
		target := b.at(ni, nj)
		if target == nil {
			return
		}
		prev := b.at(i, j)
		sc, ec := ni-i, nj-j
		candidate := cell{
			cost:       prev.cost + cost,
			exact:      prev.exact + exact,
			omissions:  prev.omissions,
			insertions: prev.insertions,
			complexity: prev.complexity + int32(max(0, sc+ec-2)),
			prevS:      int32(i), prevE: int32(j), ok: true,
		}
		if ec == 0 {
			candidate.omissions += int32(sc)
		}
		if sc == 0 {
			candidate.insertions += int32(ec)
		}
		if better(candidate, *target) {
			*target = candidate
		}
	}
	pair := func(i, j, sc, ec int) {
		var exact int32
		if sc == 1 && ec == 1 {
			if source[i] == evidence[j] {
				exact = 1
			}
		} else if source[i] == evidence[j] || source[i+sc-1] == evidence[j+ec-1] {
			// 多对多分组的首或尾已经逐字相同时，它必然可以拆成「精确 + 更小的分组」。
			// 不拒绝的话，分组惩罚按单元计、编辑距离按长度归一，逐字（汉字）粒度下
			// 把精确的邻居吞进分组反而更便宜，漏读会藏进一个模糊分组里、被标成替换。
			return
		} else if sourceBytes[i+sc]-sourceBytes[i] != evidenceBytes[j+ec]-evidenceBytes[j] || len(source[i]) == len(evidence[j]) {
			// 文本长度不同、或首单元等长却不相同（已知），就不可能是拆分或合并。
			// 先用下界剪枝，省掉大部分编辑距离计算。
			target := b.at(i+sc, j+ec)
			if target == nil {
				return
			}
			if target.ok && b.at(i, j).cost+fuzzyLowerBound(sc, ec, sourceRunes[i+sc]-sourceRunes[i], evidenceRunes[j+ec]-evidenceRunes[j]) > target.cost+costEpsilon {
				return
			}
		}
		update(i, j, i+sc, j+ec, pairedCost(source[i:i+sc], evidence[j:j+ec]), exact)
	}

	// 按「拉」的方式逐格求值：先算一对一、漏读、多读这几种便宜的前驱，得到一个
	// 好的上界，模糊分组再用下界剪枝。精确的长拆分 / 长合并仍按「推」处理，
	// 它们只会推向后面的行，读取时那些格子已经收到。
	reachable := func(i, j int) bool {
		if i < 0 || j < 0 {
			return false
		}
		c := b.at(i, j)
		return c != nil && c.ok
	}
	rowBest := make([]float64, n+1)
	rowSeen := make([]bool, n+1)
	for ni := 0; ni <= n; ni++ {
		for nj := b.lo[ni]; nj <= b.hi[ni]; nj++ {
			if ni > 0 || nj > 0 {
				if reachable(ni-1, nj-1) {
					pair(ni-1, nj-1, 1, 1)
				}
				if reachable(ni-1, nj) {
					update(ni-1, nj, ni, nj, omissionCost, 0)
				}
				if reachable(ni, nj-1) {
					update(ni, nj-1, ni, nj, insertionCost, 0)
				}
				// 远比上一行最优前缀差的格子不在任何合理路径上，只保留便宜前驱给出的值，
				// 不再算模糊分组。束宽 15 相当于连续四十多个多读，远超正常配音。
				far := ni > 0 && rowSeen[ni-1] && b.at(ni, nj).ok && b.at(ni, nj).cost > rowBest[ni-1]+beamWidth
				for sc := 1; !far && sc <= maxGroup && sc <= ni; sc++ {
					for ec := 1; ec <= maxGroup && ec <= nj; ec++ {
						if (sc > 1 || ec > 1) && reachable(ni-sc, nj-ec) {
							pair(ni-sc, nj-ec, sc, ec)
						}
					}
				}
			}
			if !b.at(ni, nj).ok {
				continue
			}
			if c := b.at(ni, nj); !rowSeen[ni] || c.cost < rowBest[ni] {
				rowBest[ni], rowSeen[ni] = c.cost, true
			}
			// 精确的拆分或合并不受分组上限约束：一个英文词被对齐工具拆成逐个字母时也要能整体配上。
			if ni < n {
				if run := exactRunLength(source[ni], evidence, nj); run > maxGroup {
					pair(ni, nj, 1, run)
				}
			}
			if nj < m {
				if run := exactRunLength(evidence[nj], source, ni); run > maxGroup {
					pair(ni, nj, run, 1)
				}
			}
		}
	}

	var groups []group
	for i, j := n, m; i > 0 || j > 0; {
		c := b.at(i, j)
		pi, pj := int(c.prevS), int(c.prevE)
		groups = append(groups, group{s0: pi, s1: i, e0: pj, e1: j, label: labelOf(source[pi:i], evidence[pj:j])})
		i, j = pi, pj
	}
	for l, r := 0, len(groups)-1; l < r; l, r = l+1, r-1 {
		groups[l], groups[r] = groups[r], groups[l]
	}
	return groups
}

func better(candidate, current cell) bool {
	if !current.ok {
		return true
	}
	if candidate.cost < current.cost-costEpsilon {
		return true
	}
	if candidate.cost > current.cost+costEpsilon {
		return false
	}
	if candidate.exact != current.exact {
		return candidate.exact > current.exact
	}
	if candidate.omissions != current.omissions {
		return candidate.omissions < current.omissions
	}
	if candidate.insertions != current.insertions {
		return candidate.insertions < current.insertions
	}
	return candidate.complexity < current.complexity
}

// labelOf 给分组定标签。与 hypit 不同，拆分与合并要求拼起来文本相同；文本不同的
// 多对一一律算替换，免得模糊配对拿到与精确拆分一样的置信度。
func labelOf(source, evidence []string) string {
	switch {
	case len(source) == 0:
		return LabelInsertion
	case len(evidence) == 0:
		return LabelOmission
	}
	if join(source) != join(evidence) {
		return LabelReplace
	}
	switch {
	case len(source) == 1 && len(evidence) == 1:
		return LabelExact
	case len(source) == 1:
		return LabelSplit
	default:
		return LabelMerge
	}
}

// prefixLengths 返回字节数与字符数的前缀和，用来 O(1) 得到任一分组的文本长度。
func prefixLengths(values []string) ([]int, []int) {
	bytes, runes := make([]int, len(values)+1), make([]int, len(values)+1)
	for i, v := range values {
		bytes[i+1] = bytes[i] + len(v)
		runes[i+1] = runes[i] + utf8.RuneCountInString(v)
	}
	return bytes, runes
}

// fuzzyLowerBound 是文本不同的多单元分组代价的下界：编辑距离至少是长度差，
// 未精确配上的单元至少是两侧单元数之差。
func fuzzyLowerBound(sc, ec, sourceLen, evidenceLen int) float64 {
	width := max(sourceLen, evidenceLen, 1)
	distance := float64(abs(sourceLen-evidenceLen)) / float64(width)
	return distance*evidenceTrust*float64(max(sc, ec)) + regroupCost*float64(sc+ec-2) + boundaryCost*float64(abs(sc-ec))
}

func join(values []string) string {
	total := 0
	for _, v := range values {
		total += len(v)
	}
	out := make([]byte, 0, total)
	for _, v := range values {
		out = append(out, v...)
	}
	return string(out)
}

// pairedCost 是非空分组的代价：字符编辑距离按长度归一，加上分组与词边界不一致的惩罚。
func pairedCost(source, evidence []string) float64 {
	sourceText, evidenceText := join(source), join(evidence)
	// 文本相同只是分段不同，代价不能随对齐工具吐出的字母个数增长。
	if sourceText == evidenceText {
		if len(source) == 1 && len(evidence) == 1 {
			return 0
		}
		return regroupCost
	}
	sr, er := []rune(sourceText), []rune(evidenceText)
	width := max(len(sr), len(er), 1)
	distance := float64(editDistance(sr, er)) / float64(width)
	// 一对一的替换不加词边界惩罚：汉字逐字成单元，一个字读错（多音字、同音字）
	// 若代价高于「漏读 + 多读」(0.85)，就会被拆成漏读，在受保护短语里误报成问题。
	if len(source) == 1 && len(evidence) == 1 {
		return distance * evidenceTrust
	}
	grouping := regroupCost * float64(max(0, len(source)+len(evidence)-2))
	exactWords := exactWordLCS(source, evidence)
	unmatched := len(source) + len(evidence) - 2*exactWords
	// 与 hypit 不同，归一化的编辑距离再乘以较长一侧的单元数。hypit 以词为单元，
	// 归一化后的距离封顶 0.8；这里汉字逐字成单元，封顶的话「早上八点」对「天」
	// 这种四字全错的分组只要 1.3，比如实报告四个漏读（2.0）还便宜，漏读就藏进了替换。
	return distance*evidenceTrust*float64(max(len(source), len(evidence))) + grouping + boundaryCost*float64(unmatched)
}

// exactWordLCS 只用于模糊分组，两侧都不超过 maxGroup 个单元。
func exactWordLCS(source, evidence []string) int {
	var rows [maxGroup + 1][maxGroup + 1]int
	for i := len(source) - 1; i >= 0; i-- {
		for j := len(evidence) - 1; j >= 0; j-- {
			if source[i] == evidence[j] {
				rows[i][j] = rows[i+1][j+1] + 1
			} else {
				rows[i][j] = max(rows[i+1][j], rows[i][j+1])
			}
		}
	}
	return rows[0][0]
}

// exactRunLength 返回从 parts[start] 起恰好拼成 target 需要几个单元，拼不成返回 0。
func exactRunLength(target string, parts []string, start int) int {
	if target == "" {
		return 0
	}
	joined := ""
	for i := start; i < len(parts); i++ {
		joined += parts[i]
		if joined == target {
			return i - start + 1
		}
		if len(joined) >= len(target) || target[:len(joined)] != joined {
			return 0
		}
	}
	return 0
}

func editDistance(left, right []rune) int {
	previous := make([]int, len(right)+1)
	current := make([]int, len(right)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(left); i++ {
		current[0] = i
		for j := 1; j <= len(right); j++ {
			substitute := previous[j-1]
			if left[i-1] != right[j-1] {
				substitute++
			}
			current[j] = min(previous[j]+1, current[j-1]+1, substitute)
		}
		previous, current = current, previous
	}
	return previous[len(right)]
}

// charPair 是字符级对齐里配上的一对下标。
type charPair struct{ source, evidence int }

// alignCharacters 是确定性的 Levenshtein 回溯，平局时对角线优先。
func alignCharacters(source, evidence []rune) []charPair {
	rows := make([][]int, len(source)+1)
	for i := range rows {
		rows[i] = make([]int, len(evidence)+1)
		rows[i][0] = i
	}
	for j := 0; j <= len(evidence); j++ {
		rows[0][j] = j
	}
	for i := 1; i <= len(source); i++ {
		for j := 1; j <= len(evidence); j++ {
			substitute := rows[i-1][j-1]
			if source[i-1] != evidence[j-1] {
				substitute++
			}
			rows[i][j] = min(rows[i-1][j]+1, rows[i][j-1]+1, substitute)
		}
	}
	var pairs []charPair
	for i, j := len(source), len(evidence); i > 0 || j > 0; {
		if i > 0 && j > 0 {
			substitute := rows[i-1][j-1]
			if source[i-1] != evidence[j-1] {
				substitute++
			}
			if rows[i][j] == substitute {
				pairs = append(pairs, charPair{i - 1, j - 1})
				i, j = i-1, j-1
				continue
			}
		}
		if i > 0 && rows[i][j] == rows[i-1][j]+1 {
			i--
			continue
		}
		j--
	}
	for l, r := 0, len(pairs)-1; l < r; l, r = l+1, r-1 {
		pairs[l], pairs[r] = pairs[r], pairs[l]
	}
	return pairs
}

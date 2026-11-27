package diff

import (
	"unicode"
	"unicode/utf8"
)

// maxWordCells bounds the word comparison of one pair of lines (the product
// of their word counts), so a minified line cannot make it quadratic in a
// costly way; such pairs get no intra-line marks.
const maxWordCells = 250_000

// maxPatchWordCells bounds the word comparisons of a whole patch. Each pair
// staying under maxWordCells is not enough: a patch loaded anyway can hold
// thousands of such pairs. Pairs the budget left cannot cover get no marks.
const maxPatchWordCells = 100 * maxWordCells

// span is a byte range of a line's text.
type span struct{ start, end int }

// pairChanges finds, in each run of deleted lines followed by added lines,
// the words that changed between the lines at the same position in both
// runs. It returns the changed ranges by line index. Each comparison spends
// from budget, which the caller shares across a patch's hunks.
func pairChanges(lines []Line, budget *int) map[int][]span {
	changes := make(map[int][]span)
	for index := 0; index < len(lines); {
		if lines[index].Kind != LineDeleted {
			index++
			continue
		}
		deletedStart := index
		for index < len(lines) && lines[index].Kind == LineDeleted {
			index++
		}
		addedStart := index
		for index < len(lines) && lines[index].Kind == LineAdded {
			index++
		}
		pairs := min(addedStart-deletedStart, index-addedStart)
		for offset := range pairs {
			old, updated := deletedStart+offset, addedStart+offset
			oldChanged, newChanged, ok := wordChanges(lines[old].Text, lines[updated].Text, budget)
			if ok {
				changes[old], changes[updated] = oldChanged, newChanged
			}
		}
	}
	return changes
}

// wordChanges compares two lines word by word and returns the ranges of
// each that the other lacks. It reports false when the lines are mostly
// different, where marking words would light up nearly everything, or when
// comparing them would cost more than the pair's cap or what is left of
// budget.
func wordChanges(old, updated string, budget *int) ([]span, []span, bool) {
	oldWords, newWords := words(old), words(updated)
	cells := len(oldWords) * len(newWords)
	if cells > maxWordCells || cells > *budget {
		return nil, nil, false
	}
	*budget -= cells
	// lengths[i][j] is the longest common run of words from oldWords[i:]
	// and newWords[j:].
	lengths := make([][]int, len(oldWords)+1)
	for i := range lengths {
		lengths[i] = make([]int, len(newWords)+1)
	}
	for i := len(oldWords) - 1; i >= 0; i-- {
		for j := len(newWords) - 1; j >= 0; j-- {
			if oldWords[i].text(old) == newWords[j].text(updated) {
				lengths[i][j] = lengths[i+1][j+1] + 1
			} else {
				lengths[i][j] = max(lengths[i+1][j], lengths[i][j+1])
			}
		}
	}
	oldKept, newKept := make([]bool, len(oldWords)), make([]bool, len(newWords))
	common := 0
	for i, j := 0, 0; i < len(oldWords) && j < len(newWords); {
		switch {
		case oldWords[i].text(old) == newWords[j].text(updated):
			oldKept[i], newKept[j] = true, true
			common += oldWords[i].end - oldWords[i].start
			i, j = i+1, j+1
		case lengths[i+1][j] >= lengths[i][j+1]:
			i++
		default:
			j++
		}
	}
	if common*2 < max(len(old), len(updated)) {
		return nil, nil, false
	}
	return unkept(oldWords, oldKept), unkept(newWords, newKept), true
}

// unkept joins the ranges of the words not kept, merging neighbours.
func unkept(words []span, kept []bool) []span {
	var ranges []span
	for index, word := range words {
		if kept[index] {
			continue
		}
		if last := len(ranges) - 1; last >= 0 && ranges[last].end == word.start {
			ranges[last].end = word.end
			continue
		}
		ranges = append(ranges, word)
	}
	return ranges
}

func (word span) text(line string) string { return line[word.start:word.end] }

// words splits a line into runs of letters, digits, and underscores, runs
// of white space, and single other characters.
func words(line string) []span {
	var result []span
	for start := 0; start < len(line); {
		first, size := utf8.DecodeRuneInString(line[start:])
		end := start + size
		if class := wordClass(first); class != 0 {
			for end < len(line) {
				next, nextSize := utf8.DecodeRuneInString(line[end:])
				if wordClass(next) != class {
					break
				}
				end += nextSize
			}
		}
		result = append(result, span{start, end})
		start = end
	}
	return result
}

// wordClass is 1 for a word character, 2 for white space, and 0 for a
// character that stands alone.
func wordClass(character rune) int {
	switch {
	case unicode.IsLetter(character) || unicode.IsDigit(character) || character == '_':
		return 1
	case unicode.IsSpace(character):
		return 2
	default:
		return 0
	}
}

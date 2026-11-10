package sessionsearch

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Typo repair. A word matches as typed, by its stem and as a prefix, so a
// half-typed word or another form of it is found; a swapped or wrong letter is
// not, and the query then finds nothing.
//
// A word that matches nothing is repaired by looking up close words in the
// index's own vocabulary, so it can only become a word some session contains:
//
//   - only plain words of typoMinRunes letters or more (phrases, 'exact and
//     ^anchored terms are taken as written);
//   - a candidate starts with the same letter, is at most typoLengthSlack
//     letters shorter, scores typoMinSimilarity or more against the word or,
//     when longer, against the word's length of its start (one swap in a
//     ten-letter word is 0.9);
//   - the closest candidate wins, then the most common.
const (
	typoMinRunes    = 4
	typoLengthSlack = 2
	// typoLongerSlack is how much longer a candidate may be when the word is
	// compared with its start (a half-typed word).
	typoLongerSlack   = 12
	typoMinSimilarity = 0.82
)

// correctQuery returns the query with misspelled plain words replaced by the
// closest word from the index vocabulary, and the corrections it made. A query
// with nothing to correct comes back unchanged.
func (s *Service) correctQuery(ctx context.Context, parsed domain.SessionSearchParsedQuery) (domain.SessionSearchParsedQuery, []domain.SessionSearchCorrection, error) {
	if s == nil || s.store == nil || len(parsed.Words) == 0 {
		return parsed, nil, nil
	}
	words := append([]string(nil), parsed.Words...)
	var corrections []domain.SessionSearchCorrection
	for index, word := range words {
		replacement, err := s.correctWord(ctx, word)
		if err != nil {
			return parsed, nil, err
		}
		if replacement != "" {
			words[index] = replacement
			corrections = append(corrections, domain.SessionSearchCorrection{From: word, To: replacement})
		}
	}
	if len(corrections) == 0 {
		return parsed, nil, nil
	}
	return parsed.WithWords(words), corrections, nil
}

// correctWord returns what a misspelled word should be searched as, or "" to
// leave it alone. Each run of letters is corrected on its own, so an
// identifier with one misspelled part ("SQILTE_BUSY", "dokcer-compose.yml")
// keeps the rest as typed.
func (s *Service) correctWord(ctx context.Context, word string) (string, error) {
	runs := letterRuns(word)
	out, changed := word, false
	for index := len(runs) - 1; index >= 0; index-- {
		run := runs[index]
		fixed, err := s.correctPart(ctx, word[run[0]:run[1]])
		if err != nil {
			return "", err
		}
		if fixed != "" {
			out = out[:run[0]] + fixed + out[run[1]:]
			changed = true
		}
	}
	if !changed {
		return "", nil
	}
	return out, nil
}

// correctPart corrects one run of letters, or returns "".
func (s *Service) correctPart(ctx context.Context, part string) (string, error) {
	folded := domain.FoldSessionSearchText(part)
	length := utf8.RuneCountInString(folded)
	if length < typoMinRunes {
		return "", nil
	}
	root := domain.SessionSearchWordRoot(part)
	found, err := s.store.SessionSearchVocabDocs(ctx, root)
	if err != nil {
		return "", err
	}
	if found != 0 {
		return "", nil
	}
	// Nothing matches as typed: any close word some session contains is
	// better than no results, so a word held by one document qualifies.
	const minDocs = 1
	first, _ := utf8.DecodeRuneInString(folded)
	candidates, err := s.store.SessionSearchVocabCandidates(ctx, string(first),
		max(1, length-typoLengthSlack), length+typoLongerSlack, minDocs)
	if err != nil {
		return "", err
	}
	typed := []rune(folded)
	best, bestScore, bestDocs := "", 0.0, int64(0)
	for _, candidate := range candidates {
		// A term the word already matches is not a correction.
		if strings.HasPrefix(candidate.Term, root) || !allLetters(candidate.Term) {
			continue
		}
		score := domain.SessionSearchSimilarity(folded, candidate.Term)
		// A word is also matched as a prefix, so a half-typed word with a typo
		// ("noatriz") is compared with the start of longer words ("notarize").
		if term := []rune(candidate.Term); len(term) > len(typed) {
			score = max(score, domain.SessionSearchSimilarity(folded, string(term[:len(typed)])))
		}
		if score < typoMinSimilarity {
			continue
		}
		if score > bestScore || (score == bestScore && candidate.Docs > bestDocs) {
			best, bestScore, bestDocs = candidate.Term, score, candidate.Docs
		}
	}
	return best, nil
}

// letterRuns returns the byte ranges of the maximal runs of letters in a word.
func letterRuns(word string) [][2]int {
	var runs [][2]int
	start := -1
	for index, r := range word {
		if unicode.IsLetter(r) {
			if start < 0 {
				start = index
			}
			continue
		}
		if start >= 0 {
			runs = append(runs, [2]int{start, index})
			start = -1
		}
	}
	if start >= 0 {
		runs = append(runs, [2]int{start, len(word)})
	}
	return runs
}

func allLetters(word string) bool {
	for _, r := range word {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	return word != ""
}

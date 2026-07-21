package log

import (
	"regexp"
	"sync"
)

// Searcher caches a compiled regular expression for bounded log searches.
type Searcher struct {
	pattern    *regexp.Regexp
	patternStr string
	mu         sync.RWMutex
}

// NewSearcher creates an empty searcher.
func NewSearcher() *Searcher {
	return &Searcher{}
}

// SetPattern compiles and stores a new expression. An empty pattern disables search.
func (s *Searcher) SetPattern(pattern string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if pattern == "" {
		s.pattern = nil
		s.patternStr = ""
		return nil
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return err
	}

	s.pattern = re
	s.patternStr = pattern
	return nil
}

// Search returns the indices of lines matched by the current expression.
func (s *Searcher) Search(lines []string) []int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.pattern == nil {
		return nil
	}

	var matches []int
	for i, line := range lines {
		if s.pattern.MatchString(line) {
			matches = append(matches, i)
		}
	}
	return matches
}

// HasPattern reports whether search is active.
func (s *Searcher) HasPattern() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pattern != nil
}

// Pattern returns the current expression text.
func (s *Searcher) Pattern() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.patternStr
}

// MatchCount returns the number of matching lines.
func (s *Searcher) MatchCount(lines []string) int {
	return len(s.Search(lines))
}

// FindNext returns the next match after currentIndex, wrapping to the beginning.
func (s *Searcher) FindNext(lines []string, currentIndex int) int {
	matches := s.Search(lines)
	for _, idx := range matches {
		if idx > currentIndex {
			return idx
		}
	}
	// Wrap navigation to the first match.
	if len(matches) > 0 {
		return matches[0]
	}
	return -1
}

// FindPrev returns the previous match before currentIndex, wrapping to the end.
func (s *Searcher) FindPrev(lines []string, currentIndex int) int {
	matches := s.Search(lines)
	prev := -1
	for _, idx := range matches {
		if idx >= currentIndex {
			break
		}
		prev = idx
	}
	// Wrap navigation to the last match.
	if prev == -1 && len(matches) > 0 {
		return matches[len(matches)-1]
	}
	return prev
}

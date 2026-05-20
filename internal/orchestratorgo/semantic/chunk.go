package semantic

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode/utf8"
)

type Chunk struct {
	Index       int
	ContentText string
	ContentHash string
}

func ChunkText(input string, maxChars, overlapChars, maxChunks int) []Chunk {
	clean := strings.TrimSpace(input)
	if clean == "" {
		return []Chunk{}
	}
	if maxChars <= 0 {
		maxChars = 2400
	}
	if overlapChars < 0 {
		overlapChars = 0
	}
	if overlapChars >= maxChars {
		overlapChars = maxChars / 5
	}
	if maxChunks <= 0 {
		maxChunks = 64
	}

	runes := []rune(clean)
	chunks := make([]Chunk, 0)
	start := 0
	for start < len(runes) && len(chunks) < maxChunks {
		end := start + maxChars
		if end > len(runes) {
			end = len(runes)
		} else {
			end = softBreak(runes, start, end)
		}
		content := strings.TrimSpace(string(runes[start:end]))
		if content != "" {
			chunks = append(chunks, Chunk{
				Index:       len(chunks),
				ContentText: content,
				ContentHash: HashText(content),
			})
		}
		if end >= len(runes) {
			break
		}
		start = end - overlapChars
		if start < 0 {
			start = 0
		}
		if start >= end {
			start = end
		}
	}
	return chunks
}

func softBreak(runes []rune, start, end int) int {
	minEnd := start + ((end - start) / 2)
	for i := end; i > minEnd; i-- {
		switch runes[i-1] {
		case '\n', '.', ';', ':':
			return i
		}
	}
	for i := end; i > minEnd; i-- {
		if runes[i-1] == ' ' || runes[i-1] == '\t' {
			return i
		}
	}
	return end
}

func HashText(input string) string {
	sum := sha256.Sum256([]byte(input))
	return hex.EncodeToString(sum[:])
}

func TruncateChars(input string, maxChars int) string {
	if maxChars <= 0 || utf8.RuneCountInString(input) <= maxChars {
		return input
	}
	runes := []rune(input)
	return string(runes[:maxChars])
}

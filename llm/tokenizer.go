package llm

import (
	"strings"
	"unicode"
)

// Tokenizer provides token counting utilities
type Tokenizer struct{}

// NewTokenizer creates a new tokenizer
func NewTokenizer() *Tokenizer {
	return &Tokenizer{}
}

// EstimateTokens estimates the number of tokens in text
// This is a simple approximation. For more accurate counting,
// integrate a proper tokenizer like tiktoken
func (t *Tokenizer) EstimateTokens(text string) int {
	// Average approximation: 1 token ≈ 4 characters
	// More sophisticated: count words and punctuation

	tokens := 0
	inWord := false

	for _, r := range text {
		if unicode.IsSpace(r) {
			if inWord {
				tokens++
				inWord = false
			}
		} else if unicode.IsPunct(r) {
			if inWord {
				tokens++
				inWord = false
			}
			tokens++ // Punctuation is usually a separate token
		} else {
			inWord = true
		}
	}

	if inWord {
		tokens++
	}

	// Add a small buffer for special tokens
	return int(float64(tokens) * 1.1)
}

// EstimateTokensSimple uses a simple character-based estimation
func (t *Tokenizer) EstimateTokensSimple(text string) int {
	// Rule of thumb: 1 token ≈ 4 characters for English text
	return len(text) / 4
}

// TruncateToTokens truncates text to approximately the specified token count
func (t *Tokenizer) TruncateToTokens(text string, maxTokens int) string {
	estimatedTokens := t.EstimateTokens(text)

	if estimatedTokens <= maxTokens {
		return text
	}

	// Calculate approximate character limit
	ratio := float64(maxTokens) / float64(estimatedTokens)
	charLimit := int(float64(len(text)) * ratio)

	if charLimit >= len(text) {
		return text
	}

	// Truncate at word boundary
	truncated := text[:charLimit]
	lastSpace := strings.LastIndex(truncated, " ")
	if lastSpace > 0 {
		truncated = truncated[:lastSpace]
	}

	return truncated + "..."
}

// CountWords counts the number of words in text
func (t *Tokenizer) CountWords(text string) int {
	words := strings.Fields(text)
	return len(words)
}

// SplitIntoSentences splits text into sentences
func (t *Tokenizer) SplitIntoSentences(text string) []string {
	// Simple sentence splitting on common terminators
	text = strings.ReplaceAll(text, "! ", "!|")
	text = strings.ReplaceAll(text, "? ", "?|")
	text = strings.ReplaceAll(text, ". ", ".|")

	sentences := strings.Split(text, "|")

	// Clean up
	var cleaned []string
	for _, s := range sentences {
		s = strings.TrimSpace(s)
		if s != "" {
			cleaned = append(cleaned, s)
		}
	}

	return cleaned
}

// GetTokenBudget calculates available token budget given constraints
func (t *Tokenizer) GetTokenBudget(maxTokens, promptTokens, bufferTokens int) int {
	available := maxTokens - promptTokens - bufferTokens
	if available < 0 {
		return 0
	}
	return available
}

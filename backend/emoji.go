package main

import (
	_ "embed"
	"regexp"
	"sort"
	"strings"
)

//go:embed brand/emoji_sequences.txt
var emojiData string

var emojiTokens = func() *regexp.Regexp {
	parts := []string{}
	for _, line := range strings.Split(emojiData, "\n") {
		if line != "" && !strings.HasPrefix(line, "#") {
			parts = append(parts, regexp.QuoteMeta(line))
		}
	}
	sort.Slice(parts, func(i, j int) bool { return len(parts[i]) > len(parts[j]) })
	// The fallback also catches newer pictographs and orphan emoji components.
	pattern := regexp.MustCompile("(?:" + strings.Join(parts, "|") + `)[\x{FE0E}\x{FE0F}]*|[\x{1F000}-\x{1FAFF}\x{200D}\x{20E3}\x{FE0E}\x{FE0F}\x{E0020}-\x{E007F}]`)
	pattern.Longest()
	return pattern
}()

var extraEmojiSpace = regexp.MustCompile(`[ \t]{2,}`)
var extraEmojiBreak = regexp.MustCompile(`\n{3,}`)

func canonicalEmoji(value string) string {
	return strings.NewReplacer("\ufe0f", "", "\ufe0e", "").Replace(value)
}

func packEmojiMap(c Company) map[string]string {
	allowed := map[string]string{}
	for _, item := range c.EmojiItems {
		if item.Emoji != "" && emojiIDPattern.MatchString(item.ID) {
			allowed[canonicalEmoji(item.Emoji)] = item.Emoji
		}
	}
	return allowed
}

func packOnlyText(text string, c Company) string {
	if c.EmojiSource != "pack" {
		return text
	}
	allowed := packEmojiMap(c)
	clean := emojiTokens.ReplaceAllStringFunc(text, func(token string) string { return allowed[canonicalEmoji(token)] })
	if clean == text {
		return text
	}
	lines := strings.Split(clean, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(extraEmojiSpace.ReplaceAllString(line, " "))
	}
	return strings.TrimSpace(extraEmojiBreak.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
}

func allowedPackEmoji(text string, c Company) bool {
	if c.EmojiSource != "pack" {
		return true
	}
	allowed := packEmojiMap(c)
	for _, token := range emojiTokens.FindAllString(text, -1) {
		if allowed[canonicalEmoji(token)] == "" {
			return false
		}
	}
	return true
}

func enforceResultEmoji(result *AIResult, c Company) {
	result.Body, result.Title = packOnlyText(result.Body, c), packOnlyText(result.Title, c)
	result.Quote = packOnlyText(result.Quote, c)
	for i, phrase := range result.Emphasis {
		result.Emphasis[i] = packOnlyText(phrase, c)
	}
}

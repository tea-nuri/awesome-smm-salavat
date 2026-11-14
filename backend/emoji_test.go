package main

import (
	"strings"
	"testing"
)

func TestSelectedPackDropsCarrotAndKeepsSupportedEmoji(t *testing.T) {
	c := Company{BrandSettings: BrandSettings{EmojiSource: "pack", EmojiItems: []CustomEmoji{{Emoji: "🍏", ID: "1"}, {Emoji: "😊", ID: "2"}, {Emoji: "⚽️", ID: "3"}}}}
	body := "🍏 Фрукты в офисе\n\nПриходите на кухню. 🥕\n\n😊 Всем приятного аппетита!\n\n#Вкоманде"
	clean := packOnlyText(body, c)
	if strings.Contains(clean, "🥕") || !strings.Contains(clean, "🍏") || !strings.Contains(clean, "😊") || !strings.Contains(clean, "Приходите на кухню.\n\n") {
		t.Fatal("wrong pack filtering or broken paragraphs")
	}
	if allowedPackEmoji(body, c) || !allowedPackEmoji(clean, c) || packOnlyText("⚽ Матч", c) != "⚽️ Матч" {
		t.Fatal("unknown emoji passed or supported variation was rejected")
	}
	c.EmojiSource = "standard"
	if packOnlyText(body, c) != body {
		t.Fatal("standard emoji mode was changed")
	}
}

func TestEmojiSequencesAreAtomicAndPlainNumbersAndHashtagsSurvive(t *testing.T) {
	c := Company{BrandSettings: BrandSettings{EmojiSource: "pack", EmojiItems: []CustomEmoji{{Emoji: "👩‍💻", ID: "1"}, {Emoji: "1️⃣", ID: "2"}, {Emoji: "👍", ID: "3"}}}}
	clean := packOnlyText("👩‍💻 Go 1.23 #Вкоманде © 2026\n\n1️⃣ Первый шаг\n\n👩‍🚀 🇷🇺 👍🏽 🥕", c)
	if clean != "👩‍💻 Go 1.23 #Вкоманде © 2026\n\n1️⃣ Первый шаг" {
		t.Fatalf("partial emoji leaked or plain text damaged: %q", clean)
	}
	if !allowedPackEmoji(clean, c) {
		t.Fatal("supported composite emoji rejected")
	}
}

func TestPackEnforcementAppliesToGenerationAndManualEdits(t *testing.T) {
	a := testApp(t)
	a.state.Companies["a"].BrandSettings = BrandSettings{EmojiSource: "pack", EmojiItems: []CustomEmoji{{Emoji: "🍏", ID: "1"}}}
	p := a.newPost("a", "Fruit post", 1, 1, nil)
	a.applyGenerationResult(p, AIResult{Kind: "post", Body: "🍏 Фрукты. 🥕", Title: "🥕 Перекус", Emphasis: []string{"🥕 Перекус"}}, "original.png")
	if p.Body != "🍏 Фрукты." || p.Title != "Перекус" || p.Emphasis[0] != "Перекус" {
		t.Fatal("generation saved an unsupported emoji")
	}
	p.Body = "Legacy draft 🥕"
	if _, err := a.action(a.user(1), p.ID, "approve", p.Revision, "", "", ""); err == nil || p.ApprovedRevision != 0 {
		t.Fatal("legacy invalid draft was approved")
	}
	if _, err := a.action(a.user(1), p.ID, "edit", p.Revision, "", "🍏 Fruit", "🍏 New text 🥕"); err != nil {
		t.Fatal(err)
	}
	if p.Body != "🍏 New text" || p.Image != "original.png" || p.Revision != 2 || p.ApprovedRevision != 0 {
		t.Fatal("manual edit did not preserve image and invalidate approval")
	}
}

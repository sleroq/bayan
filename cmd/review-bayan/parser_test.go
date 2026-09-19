package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseExportCompleteAndTruncated(t *testing.T) {
	complete := `{"name":"Chat","type":"private_group","id":-42,"messages":[{"id":1,"photo":"photos/a.jpg"}]}`
	for _, input := range []string{complete, strings.TrimSuffix(complete, "]}")} {
		info, messages, err := parseExport(strings.NewReader(input))
		if err != nil {
			t.Fatalf("parse export: %v", err)
		}
		if info.Name != "Chat" || info.ID != -42 || messages[1].Photo != "photos/a.jpg" {
			t.Fatalf("unexpected result: %#v %#v", info, messages)
		}
	}
}

func TestPairCasesClassificationLinksAndMixedMedia(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"photo.jpg", "video.mp4", "thumb.jpg"} {
		writeFixture(t, filepath.Join(dir, name), name)
	}
	messages := map[int64]message{
		10: {ID: 10, Photo: "photo.jpg"},
		20: {ID: 20, File: "video.mp4", Thumbnail: "thumb.jpg", MediaType: "video_file"},
		30: {ID: 30, ReplyToMessageID: 20, TextEntities: []textEntity{{Type: "text_link", Text: "Баян", Href: "https://t.me/c/123/10"}}},
	}
	cases := pairCases(dir, exportInfo{Name: "Chat", ID: -100123}, messages)
	if len(cases) != 1 {
		t.Fatalf("got %d cases", len(cases))
	}
	got := cases[0]
	assertEqual(t, got.OriginalID, int64(10))
	assertEqual(t, got.CurrentID, int64(20))
	assertEqual(t, got.Original.Kind, "photo")
	assertEqual(t, got.Current.Kind, "video")
	assertEqual(t, got.Original.URL, "/media/10/content")
	assertEqual(t, got.Current.URL, "/media/20/content")
	assertEqual(t, got.Current.Poster, "/media/20/poster")
	assertEqual(t, got.OriginalLink, "https://t.me/c/123/10")
	assertEqual(t, got.CurrentLink, "https://t.me/c/123/20")
	assertEqual(t, got.BotReplyLink, "https://t.me/c/123/30")
	if got.Current.Poster == "" {
		t.Fatal("video poster URL is empty")
	}
}

func writeFixture(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertEqual[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

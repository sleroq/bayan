package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type exportInfo struct {
	Name string `json:"name"`
	Type string `json:"type"`
	ID   int64  `json:"id"`
}

type textEntity struct {
	Type string `json:"type"`
	Text string `json:"text"`
	Href string `json:"href"`
}

type message struct {
	ID               int64        `json:"id"`
	Date             string       `json:"date"`
	From             string       `json:"from"`
	FromID           string       `json:"from_id"`
	ReplyToMessageID int64        `json:"reply_to_message_id"`
	Photo            string       `json:"photo"`
	File             string       `json:"file"`
	Thumbnail        string       `json:"thumbnail"`
	MediaType        string       `json:"media_type"`
	TextEntities     []textEntity `json:"text_entities"`
}

type media struct {
	Kind     string `json:"kind"`
	Path     string `json:"path"`
	URL      string `json:"url"`
	Poster   string `json:"poster,omitempty"`
	fullPath string
	poster   string
}

type reviewCase struct {
	Source       exportInfo `json:"source"`
	BotReplyID   int64      `json:"botReplyId"`
	CurrentID    int64      `json:"currentId"`
	OriginalID   int64      `json:"originalId"`
	Date         string     `json:"date"`
	From         string     `json:"from"`
	OriginalLink string     `json:"originalLink"`
	CurrentLink  string     `json:"currentLink"`
	BotReplyLink string     `json:"botReplyLink"`
	Current      media      `json:"current"`
	Original     media      `json:"original"`
}

var trailingMessageID = regexp.MustCompile(`^(.*?/)(\d+)(?:\?.*)?$`)

func loadCases(backup string) ([]reviewCase, error) {
	f, err := os.Open(filepath.Join(backup, "result.json"))
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	info, messages, err := parseExport(f)
	if err != nil {
		return nil, err
	}
	return pairCases(backup, info, messages), nil
}

func parseExport(r io.Reader) (exportInfo, map[int64]message, error) {
	dec := json.NewDecoder(r)
	var info exportInfo
	messages := make(map[int64]message)
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return info, nil, fmt.Errorf("read export object: %w", err)
	}
	for dec.More() {
		keyToken, err := dec.Token()
		if err != nil {
			return info, nil, err
		}
		key := keyToken.(string)
		err = decodeExportField(dec, key, &info, messages)
		if err != nil {
			return info, nil, fmt.Errorf("decode %s: %w", key, err)
		}
	}
	_, err = dec.Token()
	if err != nil && !errors.Is(err, io.EOF) {
		return info, nil, err
	}
	return info, messages, nil
}

func decodeExportField(dec *json.Decoder, key string, info *exportInfo, messages map[int64]message) error {
	switch key {
	case "name":
		return dec.Decode(&info.Name)
	case "type":
		return dec.Decode(&info.Type)
	case "id":
		return dec.Decode(&info.ID)
	case "messages":
		return decodeMessages(dec, messages)
	default:
		var discard json.RawMessage
		return dec.Decode(&discard)
	}
}

func decodeMessages(dec *json.Decoder, messages map[int64]message) error {
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('[') {
		return fmt.Errorf("read messages array: %w", err)
	}
	for dec.More() {
		var msg message
		if err := dec.Decode(&msg); err != nil {
			return err
		}
		messages[msg.ID] = msg
	}
	_, err = dec.Token()
	if errors.Is(err, io.EOF) { // Telegram sometimes leaves the final ]} unwritten.
		return nil
	}
	return err
}

func pairCases(backup string, info exportInfo, messages map[int64]message) []reviewCase {
	var cases []reviewCase
	for _, reply := range messages {
		originalID, _, ok := bayanReference(reply)
		if !ok {
			continue
		}
		current, currentOK := messages[reply.ReplyToMessageID]
		original, originalOK := messages[originalID]
		if !currentOK || !originalOK {
			continue
		}
		currentMedia, currentOK := localMedia(backup, current)
		originalMedia, originalOK := localMedia(backup, original)
		if !currentOK || !originalOK {
			continue
		}
		originalLink := telegramLink(info.ID, original.ID)
		paired := reviewCase{
			Source: info, BotReplyID: reply.ID, CurrentID: current.ID, OriginalID: original.ID,
			Date: current.Date, From: current.From, OriginalLink: originalLink,
			CurrentLink: telegramLink(info.ID, current.ID), BotReplyLink: telegramLink(info.ID, reply.ID),
			Current: currentMedia, Original: originalMedia,
		}
		cases = append(cases, paired)
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].BotReplyID > cases[j].BotReplyID })
	for index := range cases {
		setMediaURLs(&cases[index].Current, cases[index].CurrentID)
		setMediaURLs(&cases[index].Original, cases[index].OriginalID)
	}
	return cases
}

func bayanReference(msg message) (int64, string, bool) {
	if msg.ReplyToMessageID == 0 {
		return 0, "", false
	}
	for _, entity := range msg.TextEntities {
		if entity.Type != "text_link" || !strings.EqualFold(strings.TrimSpace(entity.Text), "Баян") {
			continue
		}
		match := trailingMessageID.FindStringSubmatch(entity.Href)
		if len(match) == 3 {
			id, err := strconv.ParseInt(match[2], 10, 64)
			return id, entity.Href, err == nil
		}
	}
	return 0, "", false
}

func localMedia(backup string, msg message) (media, bool) {
	kind, rel := "photo", msg.Photo
	if rel == "" && msg.File != "" && (msg.MediaType == "video_file" || strings.HasPrefix(msg.MediaType, "video")) {
		kind, rel = "video", msg.File
	}
	full, clean, ok := containedFile(backup, rel)
	if !ok {
		return media{}, false
	}
	m := media{Kind: kind, Path: clean, fullPath: full}
	if kind == "video" {
		if poster, _, found := containedFile(backup, msg.Thumbnail); found {
			m.poster = poster
		}
	}
	return m, true
}

func containedFile(backup, rel string) (string, string, bool) {
	if rel == "" || filepath.IsAbs(rel) {
		return "", "", false
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if outside(clean) {
		return "", "", false
	}
	root, err := filepath.EvalSymlinks(backup)
	if err != nil {
		return "", "", false
	}
	full, err := filepath.EvalSymlinks(filepath.Join(root, clean))
	if err != nil {
		return "", "", false
	}
	relToRoot, err := filepath.Rel(root, full)
	if err != nil || outside(relToRoot) {
		return "", "", false
	}
	stat, err := os.Stat(full)
	return full, filepath.ToSlash(clean), err == nil && stat.Mode().IsRegular()
}

func outside(path string) bool {
	return path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator))
}

func setMediaURLs(m *media, messageID int64) {
	m.URL = fmt.Sprintf("/media/%d/content", messageID)
	if m.poster != "" {
		m.Poster = fmt.Sprintf("/media/%d/poster", messageID)
	}
}

func telegramLink(chatID, messageID int64) string {
	chat := strconv.FormatInt(chatID, 10)
	chat = strings.TrimPrefix(chat, "-100")
	chat = strings.TrimPrefix(chat, "-")
	if chat == "" || messageID == 0 {
		return ""
	}
	return "https://t.me/c/" + chat + "/" + strconv.FormatInt(messageID, 10)
}

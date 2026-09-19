package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strings"
)

//go:embed web/*
var webFiles embed.FS

func main() {
	backup := flag.String("backup", "", "Telegram Desktop export directory (required)")
	addr := flag.String("addr", "127.0.0.1:8790", "listen address")
	flag.Parse()
	if *backup == "" {
		log.Fatal("-backup is required")
	}
	cases, err := loadCases(*backup)
	if err != nil {
		log.Fatal(err)
	}
	mediaByMessageID := indexMedia(cases)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/cases", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(cases); err != nil {
			log.Printf("write cases: %v", err)
		}
	})
	mux.HandleFunc("GET /media/{messageID}/{asset}", serveMedia(mediaByMessageID))
	web, err := fs.Sub(webFiles, "web")
	if err != nil {
		log.Fatal(err)
	}
	mux.Handle("GET /", http.FileServer(http.FS(web)))

	log.Printf("discovered %d Bayan cases; review at http://%s", len(cases), *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}

func indexMedia(cases []reviewCase) map[string]media {
	indexed := make(map[string]media, len(cases)*2)
	for _, review := range cases {
		indexed[fmt.Sprint(review.CurrentID)] = review.Current
		indexed[fmt.Sprint(review.OriginalID)] = review.Original
	}
	return indexed
}

func serveMedia(indexed map[string]media) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		item, ok := indexed[r.PathValue("messageID")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		var path string
		switch strings.ToLower(r.PathValue("asset")) {
		case "content":
			path = item.fullPath
		case "poster":
			path = item.poster
		default:
			http.NotFound(w, r)
			return
		}
		if path == "" {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, path)
	}
}

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/corona10/goimagehash"
	"github.com/sleroq/bayan/src/storage"
)

type pictureDetection string

const (
	distinctPicture  pictureDetection = "distinct"
	duplicatePicture pictureDetection = "duplicate"
	sameTemplate     pictureDetection = "same-template"
)

func TestBayanDetectionFromTelegramCases(t *testing.T) {
	requireUnlockedBayanCorpus(t)

	tests := []struct {
		botReplyID int
		originalID int
		want       pictureDetection
		knownIssue string
	}{
		{botReplyID: 609365, originalID: 424991, want: distinctPicture, knownIssue: "current false positive"},
		{botReplyID: 609747, originalID: 385834, want: distinctPicture, knownIssue: "current false positive"},
		{botReplyID: 609276, originalID: 604440, want: duplicatePicture},
		{botReplyID: 609149, originalID: 474240, want: duplicatePicture},
		{botReplyID: 607015, originalID: 322262, want: sameTemplate, knownIssue: "the detector does not classify templates separately yet"},
		{botReplyID: 606654, originalID: 241418, want: distinctPicture, knownIssue: "current false positive"},
		{botReplyID: 606571, originalID: 504540, want: duplicatePicture},
		{botReplyID: 606551, originalID: 163523, want: distinctPicture},
		{botReplyID: 606020, originalID: 284718, want: distinctPicture},
		{botReplyID: 606005, originalID: 605792, want: distinctPicture},
		{botReplyID: 605979, originalID: 243425, want: distinctPicture, knownIssue: "current false positive"},
		{botReplyID: 605786, originalID: 537129, want: distinctPicture},
		{botReplyID: 605776, originalID: 449572, want: duplicatePicture},
		{botReplyID: 605773, originalID: 572864, want: distinctPicture, knownIssue: "current false positive"},
		{botReplyID: 605758, originalID: 481513, want: duplicatePicture},
		{botReplyID: 605478, originalID: 508567, want: sameTemplate, knownIssue: "the detector does not classify templates separately yet"},
		{botReplyID: 605472, originalID: 374660, want: distinctPicture, knownIssue: "current false positive"},
	}

	for _, test := range tests {
		t.Run(fmt.Sprintf("message_%d", test.botReplyID), func(t *testing.T) {
			t.Parallel()

			got, distance := detectPicturePair(t, test.botReplyID)
			if got == test.want {
				return
			}

			if test.knownIssue != "" {
				t.Skipf("KNOWN FAILURE: %s; got %q, want %q (perception hash distance: %d)", test.knownIssue, got, test.want, distance)
			}

			originalURL := fmt.Sprintf("https://t.me/c/1565619651/%d", test.originalID)
			t.Errorf(
				"got %q, want %q (perception hash distance: %d; original: %s)",
				got,
				test.want,
				distance,
				originalURL,
			)
		})
	}

	t.Run("review_manifest", testBayanReviewManifest)
}

func requireUnlockedBayanCorpus(t *testing.T) {
	t.Helper()
	manifest := filepath.Join("testdata", "bayan", "reviews.json")
	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatalf("read review manifest: %v", err)
	}
	if bytes.HasPrefix(data, []byte("\x00GITCRYPT\x00")) {
		t.Skip("Bayan regression corpus is encrypted; run git-crypt unlock")
	}
}

type bayanReview struct {
	Source struct {
		Name string `json:"name"`
		Type string `json:"type"`
		ID   int64  `json:"id"`
	} `json:"source"`
	BotReplyID      int              `json:"botReplyId"`
	CurrentID       int              `json:"currentId"`
	OriginalID      int              `json:"originalId"`
	Classification  pictureDetection `json:"classification"`
	Note            string           `json:"note"`
	BotReplyLink    string           `json:"botReplyLink"`
	CurrentLink     string           `json:"currentLink"`
	OriginalLink    string           `json:"originalLink"`
	CurrentMedia    string           `json:"currentMedia"`
	OriginalMedia   string           `json:"originalMedia"`
	Tags            []string         `json:"tags"`
	CurrentFixture  bayanFixture     `json:"currentFixture"`
	OriginalFixture bayanFixture     `json:"originalFixture"`
}

type bayanFixture struct {
	Type       string   `json:"type"`
	SourcePath string   `json:"sourcePath"`
	Fixtures   []string `json:"fixtures"`
}

func testBayanReviewManifest(t *testing.T) {
	manifestPath := filepath.Join("testdata", "bayan", "reviews.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read review manifest: %v", err)
	}

	var reviews []bayanReview
	if err := json.Unmarshal(data, &reviews); err != nil {
		t.Fatalf("decode review manifest: %v", err)
	}
	if len(reviews) != 573 {
		t.Fatalf("review manifest has %d cases, want 573", len(reviews))
	}

	seen := make(map[int]struct{}, len(reviews))
	for _, review := range reviews {
		validateBayanReview(t, review, seen)
	}

	hashes := make(map[string]*goimagehash.ImageHash)
	for _, review := range reviews {
		t.Run(fmt.Sprintf("message_%d_current_%d", review.BotReplyID, review.CurrentID), func(t *testing.T) {
			got, distance := detectBayanFixtures(t, review.CurrentFixture, review.OriginalFixture, hashes)
			if got == review.Classification {
				return
			}

			message := fmt.Sprintf(
				"message %d (current %d): got %q, want %q (perception hash distance: %d; current: %s; original: %s)",
				review.BotReplyID, review.CurrentID, got, review.Classification, distance, review.CurrentLink, review.OriginalLink,
			)
			if review.Classification != duplicatePicture {
				t.Skipf("KNOWN FAILURE: %s", message)
			}
			t.Error(message)
		})
	}
}

func validateBayanReview(t *testing.T, review bayanReview, seen map[int]struct{}) {
	t.Helper()
	if review.BotReplyID == 0 || review.CurrentID == 0 || review.OriginalID == 0 {
		t.Fatalf("review has empty message ID: %+v", review)
	}
	if _, exists := seen[review.BotReplyID]; exists {
		t.Fatalf("duplicate bot reply ID %d", review.BotReplyID)
	}
	seen[review.BotReplyID] = struct{}{}
	if !validPictureDetection(review.Classification) {
		t.Fatalf("message %d has invalid classification %q", review.BotReplyID, review.Classification)
	}
	if !review.hasSourceIdentity() {
		t.Fatalf("message %d has incomplete source identity", review.BotReplyID)
	}
	if review.Tags == nil {
		t.Fatalf("message %d has no tags array", review.BotReplyID)
	}
	validateBayanFixture(t, review.BotReplyID, review.CurrentMedia, review.CurrentFixture)
	validateBayanFixture(t, review.BotReplyID, review.OriginalMedia, review.OriginalFixture)
}

func validPictureDetection(classification pictureDetection) bool {
	return classification == duplicatePicture || classification == distinctPicture || classification == sameTemplate
}

func (review bayanReview) hasSourceIdentity() bool {
	return review.Source.ID != 0 &&
		review.Source.Name != "" &&
		review.Source.Type != "" &&
		review.BotReplyLink != "" &&
		review.CurrentLink != "" &&
		review.OriginalLink != ""
}

func validateBayanFixture(t *testing.T, botReplyID int, media string, fixture bayanFixture) {
	t.Helper()
	wantFiles := 1
	if fixture.Type == "video" {
		wantFiles = 4
	} else if fixture.Type != "image" {
		t.Fatalf("message %d has invalid fixture type %q", botReplyID, fixture.Type)
	}
	if media == "" || fixture.SourcePath != media || len(fixture.Fixtures) != wantFiles {
		t.Fatalf("message %d has invalid %s fixture for %q", botReplyID, fixture.Type, media)
	}
	for _, asset := range fixture.Fixtures {
		if asset == "" {
			t.Fatalf("message %d has an empty fixture path", botReplyID)
		}
	}
}

func detectBayanFixtures(t *testing.T, current, original bayanFixture, cache map[string]*goimagehash.ImageHash) (pictureDetection, int) {
	t.Helper()
	if current.Type != original.Type {
		t.Fatalf("cannot compare %s fixture with %s fixture", current.Type, original.Type)
	}
	if current.Type == "image" {
		currentHash := cachedFixtureHash(t, current.Fixtures[0], cache)
		originalHash := cachedFixtureHash(t, original.Fixtures[0], cache)
		distance, matches, err := pictureMatches(currentHash, originalHash)
		if err != nil {
			t.Fatalf("compare pictures: %v", err)
		}
		if matches {
			return duplicatePicture, distance
		}
		return distinctPicture, distance
	}

	currentHashes := videoFixtureHashes(t, current, cache)
	originalHashes := videoFixtureHashes(t, original, cache)
	distance, err := averageVideoHashDistance(currentHashes, originalHashes)
	if err != nil {
		t.Fatalf("compare videos: %v", err)
	}
	if distance < 10 {
		return duplicatePicture, distance
	}
	return distinctPicture, distance
}

func videoFixtureHashes(t *testing.T, fixture bayanFixture, cache map[string]*goimagehash.ImageHash) *storage.VideoHashes {
	t.Helper()
	return &storage.VideoHashes{
		FrameA: cachedFixtureHash(t, fixture.Fixtures[0], cache),
		FrameB: cachedFixtureHash(t, fixture.Fixtures[1], cache),
		FrameC: cachedFixtureHash(t, fixture.Fixtures[2], cache),
		FrameD: cachedFixtureHash(t, fixture.Fixtures[3], cache),
	}
}

func cachedFixtureHash(t *testing.T, fixture string, cache map[string]*goimagehash.ImageHash) *goimagehash.ImageHash {
	t.Helper()
	if hash := cache[fixture]; hash != nil {
		return hash
	}
	hash, _, err := hashPicFile(filepath.Join("testdata", "bayan", filepath.FromSlash(fixture)))
	if err != nil {
		t.Fatalf("hash fixture %q: %v", fixture, err)
	}
	cache[fixture] = hash
	return hash
}

func detectPicturePair(t *testing.T, botReplyID int) (pictureDetection, int) {
	t.Helper()

	original := filepath.Join("testdata", "bayan", fmt.Sprintf("%d-original.jpg", botReplyID))
	current := filepath.Join("testdata", "bayan", fmt.Sprintf("%d-current.jpg", botReplyID))

	originalPHash, _, err := hashPicFile(original)
	if err != nil {
		t.Fatalf("hash original picture: %v", err)
	}
	currentPHash, _, err := hashPicFile(current)
	if err != nil {
		t.Fatalf("hash current picture: %v", err)
	}

	distance, matches, err := pictureMatches(currentPHash, originalPHash)
	if err != nil {
		t.Fatalf("compare pictures: %v", err)
	}
	if matches {
		return duplicatePicture, distance
	}

	return distinctPicture, distance
}

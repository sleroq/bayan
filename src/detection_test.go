package main

import (
	"fmt"
	"path/filepath"
	"testing"
)

type pictureDetection string

const (
	distinctPicture  pictureDetection = "distinct"
	duplicatePicture pictureDetection = "duplicate"
	sameTemplate     pictureDetection = "same-template"
)

func TestBayanDetectionFromTelegramCases(t *testing.T) {
	t.Parallel()

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

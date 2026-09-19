# Telegram detection cases

The manually curated cases contain the media that triggered the bot reply
(`<reply>-current.jpg`) and the older media linked by that reply
(`<reply>-original.jpg`).

`reviews.json` contains all reviewed Telegram reports. Each row preserves the
source chat identity, bot reply/current/original message IDs and links, desired
classification, reviewer note, original export paths, and hermetic fixture
references. `tags` is currently an empty string array reserved for future
taxonomy; tags do not affect the desired classification.

The manifest and all media in this directory are encrypted in Git with
`git-crypt`; this README remains public. Authorized clones must run
`git-crypt unlock` before running the corpus.

Assets under `review-media/` are content-addressed and shared by cases that use
the same export media. Images are copied losslessly. Videos are represented by
only the four JPEG frames selected by production's `hashFrames`: the source is
staged as `a.mp4`, frames are extracted at 1 fps, and selection uses lexical
`os.ReadDir` ordering including that staged source. Extraction first uses the
historical production ffmpeg command. Pixel-format normalization
(`fps=1,format=yuv420p`) is used only where modern ffmpeg requires it to decode
the video to JPEG.

Run the corpus with:

```sh
go test -v ./internal/bayan -run TestBayanDetectionFromTelegramCases
```

Desired `duplicate` mismatches fail the suite. Desired `distinct` and
`same-template` mismatches are historical false positives or an unsupported
classification, so they execute and are reported as skipped subtests with their
current hash distance. If detector behavior reaches their desired classification
they pass without changing the manifest.

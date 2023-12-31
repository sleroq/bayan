# Bayan

Say goodbye to seeing meme déjà vu! Bayan is here to keep your meme content fresher than an untouched avocado. Got a friend who forwards the same 'funny' cat video from 2007? Bayan is on it like white on rice.

Acting as your friendly neighborhood duplicate detective, Bayan checks every new image or video shared in chat with all the old ones. Come across a second sneak of the same sneezing panda? Not on Bayan’s watch! This bot will highlight the copycats faster than you feel déjà vu.

## Features
- Detects duplicate images and videos (even with watermark)
- Detects difference in videos with same length and thumbnail
- Does not store any images or videos

## How to use Bayan

1. Add Bayan to your group chat
2. Done.

## How to run Bayan

### Dependencies

- [go](https://golang.org/doc/install)
- [ffmpeg](https://www.ffmpeg.org/download.html) (for video processing)

1. `cp scripts/env.bash.example scripts/env.bash`
2. Fill in the blanks in `scripts/env.bash`
3. Start bot with `./scripts/run.bash`
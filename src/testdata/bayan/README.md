# Telegram detection cases

Each case contains the media that triggered the bot reply (`<reply>-current.jpg`)
and the older media linked by that reply (`<reply>-original.jpg`). The table in
`../../detection_test.go` records the desired classification and any known
failure in the current detector.

Run the corpus with:

```sh
go test -v ./src -run TestBayanDetectionFromTelegramCases
```

Known failures are executed and then reported as skipped subtests, including
their current hash distance. Remove `knownIssue` when a detector change makes a
case pass. New cases should preserve the Telegram bot reply and original message
IDs in the test table so their source remains traceable.

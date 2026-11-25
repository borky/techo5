# This fork

A fork of [HuskerMinion/techo5](https://github.com/HuskerMinion/techo5). Most of what is done here is
meant to go upstream; this file says what has not gone yet, what stays in this fork on purpose, and how
the branches are kept so that both stay easy to carry across upstream's releases.

This file lives on `develop` only. It is never part of a branch proposed upstream.

## Branches

- **`main`**: a mirror of upstream's `main`, nothing else. Never commit to it. Bring it up to date with
  `git fetch upstream && git merge --ff-only upstream/main && git push origin main`.
- **`develop`**: `main` plus every branch below merged in. This is what the devices here run. When
  `main` moves, merge `main` into `develop` (or rebuild it on a fresh `main` and merge the still-open
  branches again).
- **One branch per upstream pull request**, cut from `main`, never from `develop`: one feature or fix
  each, rebased on `main` before the pull request is opened or updated, author and committer
  `TECHO5 contributor <contributor@example.com>`, written to upstream's
  conventions (commit style, docs, CI checks for every build tag). Merged into `develop` too, so the
  devices get it before upstream takes it.
- **`fork/<name>`**: a feature kept in this fork on purpose. Cut from `main`, merged into `develop`,
  never proposed upstream. Each one has an entry under "Fork-only features" below.

Once upstream merges a pull request, `main` is fast-forwarded, the branch is deleted, and its entry
moves from "Waiting to go upstream" to "Merged upstream".

## Waiting to go upstream

- **Voice Quality** (`feat/voice-quality`, not yet proposed). A select in Home Assistant and on the
  setup page: replies and announcements asked of Home Assistant and played at Standard (16 kHz, the
  default and unchanged), High (24 kHz, Home Assistant Cloud's own rate) or Highest (48 kHz). A
  band-limited converter replaces straight lines wherever speech changes rate at the higher qualities.
  Reviewed once; whether Home Assistant serves pipeline replies (not only announcements) at the chosen
  rate is still to be checked on a device. Rebuilt on v1.6.53 as one commit: upstream's reply player
  change (#715) takes the speaker out of the offer the same way, so Voice Quality now goes through
  upstream's `voice.Offer`, `media.FormatsFor(elsewhere, rate)` and `describeFormats` instead of a
  record of its own; a reply player still gets 48 kHz whatever the quality.

- **Stock volume curve** (`feat/stock-volume-curve`, not yet proposed). A switch, off by default, in
  Home Assistant and on the setup page, on the Echo Show 5 1st gen with its speaker tuning: each volume
  step goes into the tuning as hot as under Fire OS (its curve measured, and checked digitally against
  Fire OS's audio HAL, whose processing matches `lib/asp` within half a decibel). The usual curve stops
  about where Fire OS's step 15 is, so the same loudness needed a higher step and a flatter EQ bucket.
  Reviewed once; listened to on a device: as loud and full as Fire OS at the same step.

- **Residual echo suppressor** (`feat/residual-echo-suppressor`, not yet proposed). After the
  built-in echo canceller, each frequency is turned down by the echo the filter predicted there times
  how much of it usually leaks through (learned over two seconds), never more than 10 dB. Behind the
  switch **Microphone echo suppression** in Home Assistant, off by default. Offline on a recording
  from the Echo Show 5 1st gen it heard 7-8 wakes out of the same two minutes instead of 5, none on
  music alone; live it was not clearly better, so it waits for an A/B test with the switch. Reviewed,
  the findings fixed; committed under a neutral author.

## Fork-only features

Each entry here says what the feature does, why it stays in this fork, how to turn it on, and which
files it touches, so it can be carried across upstream updates.

- **Raw capture** (`fork/raw-capture`, one commit on `main`). A diagnostic: every microphone and the
  playback reference, exactly as captured, written to a WAVE file on the device, so the echo canceller
  and the wake word can be tried offline on one recording. Fork-only because it is a developer's tool
  with no setting or UI, and upstream has its own capture tools per device. Off until asked: write a
  number of seconds (at most 180) to `/data/misc/techo5/raw-capture.request`; the file is removed as
  the recording starts, and `raw-capture.wav` appears in the same directory when it ends (the log says
  "raw capture written"). Copy it off and delete it; nothing sends it anywhere. Files:
  `echod/internal/hardware/mic/rawcapture.go`, `rawcapture_test.go`, one call in `mic.go`
  (`broadcastSelected`, after the software mute).

## Planned

- Nothing at the moment. (A louder, livelier speaker on the Echo Show 5 1st gen is now Stock volume
  curve, above: the Show's AFE.cfg bypasses AVL, so that stage was never the difference.)

## Merged upstream

- **Echo reference on the Echo Show 5 1st gen** (`fix/checkers-echo-reference`, upstream #717, in
  v1.6.53). The 1st gen Show 5 cancels echo against `lib/echoref`'s software reference, read after read
  carrying on and held in place. Upstream changed one thing: a placement more than 2 ms away (`settle`,
  ours was 20 ms `keep`) for 25 reads in a row is followed, in case the capture's clock drifts from the
  speaker's; and a read with no delay rejoins. To check on the device that the reference still holds
  still over music (a raw capture and the fixed-response test). The branch can be deleted.

- **Romanian as a screen language** (`locale-romanian`, upstream #684, in v1.6.49). The branch can be
  deleted.

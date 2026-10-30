# Review notes: clip_v6.mp4 (v6)

video /videos/clip_v6.mp4 (1920x1080, 30.0 fps, 01:00.0) · family v5 v6  
2 takes · 7 segments · 0:30 recorded · whisper small (cpu) · 4 moments · 2 strokes · 6 flags

## Rules for applying these notes
- Later statements override earlier ones: a higher #number was said later.
- A retraction voids the comments it refers to: the same clip/moment, unless Sam names a wider scope.
- A reinstatement restores what that retraction voided.
- When the scope is unclear, ask Sam rather than guess. Flags are heuristics, and nothing was deleted.
- `[v7 flip]` = said while Sam had flipped (C) to another version to compare: it is about that version vs v6. `[v3]` on a v6 note = recorded on v3 in a range whose frames are identical.
- `(audio)` = the line is about sound (VO pauses, music, volume): check the audio, not just the picture.

How to read: #n = spoken order. S2 = recording segment (R on to R off; only these were recorded). [mm:ss.s] = video time. `paused` = said while stopped on that frame. `playing a–b` = said while the video ran (allow ~1 s reaction lag). Strokes use normalized coords, (0,0) top-left to (1,1) bottom-right, and match the numbered badges in each moment's snapshot. notes.json has everything plus narration positions.

## Flags: retractions and reinstatements (6)
- #3 S2 · M1 · "Throw out everything I said about this clip." · FLAG retraction (everything earlier on this clip): would void #1, #2
- #5 S4 · M1 · "Actually, add that back." · FLAG reinstatement: undoes #3, restoring #1, #2
- #6 S5 · M3 · "The caption is too small. Scrap that." · FLAG retraction (the comment just before): would void #5
- #8 S6 · M4 · "Ignore that cut comment, it is fine." · FLAG retraction (this clip): would void #7
- #9 S6 · M4 · "The header is great. Forget what I said so far." · FLAG retraction (possibly wider than this clip: ask Sam): would void #7
- #10 S7 · M2 · "Never mind, keep the zoom. Remove that comment about the header. Restore it." · FLAG reinstatement: undoes #9, restoring #7

## By moment (video order)
### M1 · 00:08.5–00:10.8 · no snapshot
- #1 S1 [v6] · playing 00:08.5–00:09.7 · "Look at the stats."
- #2 S1 [v6] · paused 00:10.0 · "This zoom is too slow." · stroke 1: red line (0.40,0.40)→(0.60,0.60) heading down-right
- #3 S2 [v6] · paused 00:08.8 · "Throw out everything I said about this clip." · FLAG retraction (everything earlier on this clip): would void #1, #2
- #4 S3 [v5 flip] · paused 00:10.8 · "This was better in v5."
- #5 S4 [v6] · paused 00:10.8 · "Actually, add that back." · stroke 2: green line (0.10,0.20)→(0.30,0.20) heading right · FLAG reinstatement: undoes #3, restoring #1, #2

### M2 · 00:20.0 · no snapshot
- #10 S7 [v6] · paused 00:20.0 · "Never mind, keep the zoom. Remove that comment about the header. Restore it." · FLAG reinstatement: undoes #9, restoring #7

### M3 · 00:30.0 · no snapshot
- #6 S5 [v6] · paused 00:30.0 · "The caption is too small. Scrap that." · FLAG retraction (the comment just before): would void #5

### M4 · 00:44.9–00:48.5 · no snapshot
- #7 S6 [v6] · playing 00:44.9–00:46.0 · "The cut here feels late."
- #8 S6 [v6] · playing 00:46.5–00:48.0 · "Ignore that cut comment, it is fine." · FLAG retraction (this clip): would void #7
- #9 S6 [v6] · paused 00:48.5 · "The header is great. Forget what I said so far." · FLAG retraction (possibly wider than this clip: ask Sam): would void #7

## In the order you said it
### S1 · take 2026-01-10_10-00-00 0:00–0:06 · video 00:08.0–00:10.0 · 2 comments
- #1 S1 [v6] · playing 00:08.5–00:09.7 · "Look at the stats." · M1
- #2 S1 [v6] · paused 00:10.0 · "This zoom is too slow." · stroke 1 · M1

### S2 · take 2026-01-10_10-00-00 0:11–0:14 · video 00:08.8–00:08.8 · 1 comment
- #3 S2 [v6] · paused 00:08.8 · "Throw out everything I said about this clip." · FLAG retraction (everything earlier on this clip): would void #1, #2 · M1

### S3 · take 2026-01-10_10-00-00 0:18–0:20 · video 00:10.8–00:10.8 · 1 comment
- #4 S3 [v5 flip] · paused 00:10.8 · "This was better in v5." · M1

### S4 · take 2026-01-10_10-00-00 0:22–0:25 · video 00:10.8–00:10.8 · 1 comment
- #5 S4 [v6] · paused 00:10.8 · "Actually, add that back." · stroke 2 · FLAG reinstatement: undoes #3, restoring #1, #2 · M1

### S5 · take 2026-01-10_11-30-00 0:01–0:04 · video 00:30.0–00:30.0 · 1 comment
- #6 S5 [v6] · paused 00:30.0 · "The caption is too small. Scrap that." · FLAG retraction (the comment just before): would void #5 · M3

### S6 · take 2026-01-10_11-30-00 0:06–0:13 · video 00:44.5–00:44.5 · 3 comments
- #7 S6 [v6] · playing 00:44.9–00:46.0 · "The cut here feels late." · M4
- #8 S6 [v6] · playing 00:46.5–00:48.0 · "Ignore that cut comment, it is fine." · FLAG retraction (this clip): would void #7 · M4
- #9 S6 [v6] · paused 00:48.5 · "The header is great. Forget what I said so far." · FLAG retraction (possibly wider than this clip: ask Sam): would void #7 · M4

### S7 · take 2026-01-10_11-30-00 0:16–0:21 · video 00:20.0–00:20.0 · 1 comment
- #10 S7 [v6] · paused 00:20.0 · "Never mind, keep the zoom. Remove that comment about the header. Restore it." · FLAG reinstatement: undoes #9, restoring #7 · M2

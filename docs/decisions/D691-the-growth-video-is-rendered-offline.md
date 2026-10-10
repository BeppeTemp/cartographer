---
topic: control-plane
---

# D691 — The growth video is rendered offline, at 60 fps, in the background

**Decision.** *Export video* no longer records the visible replay. It renders
the replay on a second `LivingScene` in a detached container, advanced by a
virtual clock of exactly 1000/60 ms a frame and drawn at twice the export size;
each frame is downsampled into an export-size 2D canvas, overlaid, and encoded
with WebCodecs (`VideoEncoder`) into an MP4 (H.264 High 4.2) or, where H.264 is
not encoded, a WebM (VP9), muxed by `mediabunny`. The clip opens on a 1 s
logo card, so frame 0 is the thumbnail; the replay keeps its own pace
(`growthPace`) in video time. It supersedes the recording mechanism of
[D677](D677-the-growth-replay-exports-as-a-browser-recorded-video.md); the
dialog, aspects, overlay and file names stay.

**Why.** Real-time capture ties the clip to the display: it stops in a hidden
tab, runs at whatever rate the page paints, and leaves `MediaRecorder` the
choice of rate control and of the first frame (the videos showed black as a
share preview). An offline render has none of those limits, and costs the user
nothing to watch: the visible scene and camera are never touched, so the
replay, the graph and the rest of the Atlas stay usable while it runs and a
switch of KB or Map does not end it (the export owns its scene and its data).

**Alternatives rejected.**

- Keeping `MediaRecorder`: real-time only, no control over rate or first frame.
- ffmpeg.wasm: about 30 MB for a share button (as in D677).
- A server-side encoder: the Atlas is read-only and the server has none.
- Letting `mediabunny`'s own `CanvasSource` drive the encoder: it hides the
  encoder queue, which is the one brake the render loop needs.
- Pacing the loop with `setTimeout`: timers are throttled to once a second in
  a hidden tab; the loop awaits `dequeue` events and a `MessageChannel` message
  (never throttled) instead.

**Consequences.**

- `LivingScene` reads time only through `clock()`. A scene built with the
  `offscreen` option has no frame loop, `ResizeObserver`, `visibilitychange`
  listener or input, a fixed size, and no burst or settle camera; `renderFrame`
  advances it. `beginCapture`/`endCapture` are gone.
- At most one offline scene exists at a time (a second export is refused), and
  it is disposed with its WebGL context (`forceContextLoss`) on done, cancel,
  error and unmount: a test holds it.
- The 2D canvas copy of the WebGL canvas must happen in the task that rendered
  it (the renderer keeps no drawing buffer); only after that may the loop await.
- Frames are rendered at 2x the export size: a 1920x1080 export draws
  3840x2160, so the GPU must allow it. Artifacts (diamonds) are not drawn in
  the video, which is of concepts only.
- Chromium without H.264 (and Firefox) gets WebM; the browser suite asserts the
  extension the browser reports, decodes the downloaded file in a page, and
  checks the opening card, a mid-replay frame, the duration and the 1/60 s
  frame step, with the page hidden halfway through the export.

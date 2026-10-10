---
topic: control-plane
---

# D677 — The growth replay exports as a browser-recorded video

**Decision.** The Atlas records the growth replay (D367) in the browser with
`MediaRecorder` and downloads the clip: MP4 (H.264) where the browser records
it, WebM where it does not. Each frame is composed on a 2D canvas, the scene's
WebGL canvas plus an overlay drawn in the frame itself (KB name, replayed date,
concept count, an end card), at 16:9, 9:16 or 1:1, chosen at export time.
The server is not involved.

**Why.** The Atlas is read-only and its server has no encoder; a clip to share
should cost the operator one click and no install. The overlay is drawn on the
canvas, not the DOM, because only a canvas stream can carry it into the file.

**Alternatives rejected.**

- ffmpeg.wasm: about 30 MB of wasm for a share button.
- A server-side encoder: the server would need ffmpeg, and the read-only API
  would gain a rendering job.
- Recording the on-screen canvas as it is: its size and aspect are the
  window's, not the platform's.
- `preserveDrawingBuffer` on the renderer, to copy the canvas at any time: it
  costs every frame of the live view for a feature used rarely. The copy
  happens in the scene's frame listeners, right after the render.

**Consequences.**

- While exporting, the scene is in capture mode (`beginCapture`/`endCapture`):
  the renderer and camera take the frame's size and the fit reads that frame,
  not the container; `endCapture` must restore the view exactly, whatever ends
  the export.
- Chromium without a proprietary codec (Playwright's, Firefox) records WebM
  only, so the browser suite asserts the extension the browser reports and the
  MP4 choice is covered by the unit test of `pickMimeType`.
- A hidden tab stops the render loop; the export pauses with it rather than
  record frozen frames, so the tab must stay visible while it records.
- Node labels are DOM and are not in the clip, by design.

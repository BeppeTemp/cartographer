// The film's soundtrack, synthesised in code so it is ours to publish and follows the story: a warm
// pad and a soft FM electric piano in D major, a bell when a note becomes a node, a chime when Marco's
// agent reads Alice's note, a muted knock when Leo's broken link bounces, a low swell under the logo.
// No dependency: it writes a 48 kHz stereo 16-bit WAV that record.mjs muxes into film.mp4.
import fs from "node:fs";

const SR = 48000;
const BEAT = 60 / 72; // 72 BPM
const midi = (m) => 440 * 2 ** ((m - 69) / 12);

// Chord voicings (MIDI), D major. The bass is the first note an octave down.
const CH = {
  Dmaj9: [50, 57, 61, 64, 66],
  Bm9: [47, 54, 57, 61, 62],
  Gmaj9: [43, 50, 54, 57, 59],
  A6: [45, 52, 57, 61, 66],
  Em9: [52, 59, 62, 66, 67],
  A7sus4: [45, 52, 55, 62, 64],
};

/** A deterministic PRNG, so every render sounds the same. */
function rng(seed) {
  return () => {
    seed = (seed + 0x6d2b79f5) | 0;
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export function renderSoundtrack(cues, file) {
  const N = Math.ceil(cues.duration * SR);
  const L = new Float32Array(N);
  const R = new Float32Array(N);
  const rand = rng(7);
  const add = (i, l, r) => {
    if (i >= 0 && i < N) {
      L[i] += l;
      R[i] += r;
    }
  };

  // --- chord timeline -------------------------------------------------------------------
  const [team, wiki, monday, tuesday, everyday, end] = cues.scenes;
  const timeline = [
    [team, "Dmaj9"], [team + (wiki - team) / 2, "Bm9"],
    [wiki, "Gmaj9"],
    [monday, "Dmaj9"], [monday + 4 * BEAT, "A6"], [monday + 8 * BEAT, "Bm9"], [monday + 12 * BEAT, "Gmaj9"],
    [tuesday, "Em9"], [cues.answer - 2 * BEAT, "A7sus4"], [cues.answer, "Dmaj9"], [cues.answer + 4 * BEAT, "Gmaj9"],
    [everyday, "Dmaj9"], [everyday + 3 * BEAT, "Gmaj9"], [everyday + 6 * BEAT, "Bm9"], [everyday + 9 * BEAT, "A6"],
    [end, "Gmaj9"], [end + 4 * BEAT, "Dmaj9"],
  ].sort((a, b) => a[0] - b[0]);
  const segments = timeline.map(([t0, name], k) => ({ t0, t1: k + 1 < timeline.length ? timeline[k + 1][0] : cues.duration, notes: CH[name] }));

  // --- pad: two detuned additive voices per note, slow attack, crossfaded release -------
  for (const { t0, t1, notes } of segments) {
    notes.forEach((m, n) => {
      const pan = 0.5 + (n - 2) * 0.12;
      for (const cents of [-4, 4]) {
        const f = midi(m) * 2 ** (cents / 1200);
        const i0 = Math.floor(t0 * SR);
        const i1 = Math.min(N, Math.floor((t1 + 2.2) * SR));
        for (let i = i0; i < i1; i++) {
          const t = i / SR - t0;
          const held = t1 - t0;
          const env = Math.min(1, t / 1.4) * (t < held ? 1 : Math.exp(-(t - held) / 0.7));
          const ph = 2 * Math.PI * f * (i / SR);
          const s = (Math.sin(ph) + 0.22 * Math.sin(2 * ph) + 0.06 * Math.sin(3 * ph)) * env * 0.022;
          add(i, s * (1 - pan) * 1.2, s * pan * 1.2);
        }
      }
    });
    // bass: the root, folded into E2..D#3 so it stays warm on laptop speakers and never booms
    let root = notes[0];
    while (root > 51) root -= 12;
    while (root < 40) root += 12;
    const fb = midi(root);
    for (let i = Math.floor(t0 * SR); i < Math.min(N, Math.floor((t1 + 1.2) * SR)); i++) {
      const t = i / SR - t0;
      const env = Math.min(1, t / 0.5) * (t < t1 - t0 ? 1 : Math.exp(-(t - (t1 - t0)) / 0.4));
      const ph = 2 * Math.PI * fb * (i / SR);
      const s = (Math.sin(ph) + 0.25 * Math.sin(2 * ph)) * env * 0.035;
      add(i, s, s);
    }
  }

  // --- FM electric piano -----------------------------------------------------------------
  const epiano = (time, m, vel, pan = 0.5) => {
    const f = midi(m);
    const i0 = Math.floor(time * SR);
    for (let k = 0; k < 3.2 * SR; k++) {
      const t = k / SR;
      const index = 1.6 * Math.exp(-t * 5) + 0.25;
      const tine = 0.35 * Math.exp(-t * 18) * Math.sin(2 * Math.PI * f * 14 * t);
      const s = Math.sin(2 * Math.PI * f * t + index * Math.sin(2 * Math.PI * f * t) + tine) * Math.min(1, t / 0.004) * Math.exp(-t / 1.3) * vel;
      add(i0 + k, s * (1 - pan) * 1.4, s * pan * 1.4);
    }
  };
  const arp = [1, 3, 2, 4, 3, 2];
  for (const { t0, t1, notes } of segments) {
    const fast = t0 >= everyday && t0 < end;
    const slow = t0 < monday || t0 >= end;
    const step = fast ? BEAT / 2 : slow ? 2 * BEAT : BEAT;
    let k = 0;
    for (let t = t0 + (slow ? BEAT : 0); t < t1 - 0.05; t += step, k++) {
      const m = notes[arp[k % arp.length]] + 12;
      const jitter = (rand() - 0.5) * 0.02;
      epiano(t + jitter, m, (fast ? 0.05 : 0.065) * (0.85 + 0.3 * rand()), 0.35 + 0.3 * rand());
    }
  }

  // --- cues ------------------------------------------------------------------------------
  const bell = (time, m, vel) => {
    const f = midi(m);
    const i0 = Math.floor(time * SR);
    for (let k = 0; k < 4 * SR; k++) {
      const t = k / SR;
      const index = 2.4 * Math.exp(-t * 2.5);
      const s = (Math.sin(2 * Math.PI * f * t + index * Math.sin(2 * Math.PI * f * 3.5 * t)) + 0.5 * Math.sin(Math.PI * f * t)) * Math.min(1, t / 0.003) * Math.exp(-t / 1.6) * vel;
      add(i0 + k, s * 0.9, s * 1.1);
    }
  };
  [81, 78, 83].forEach((m, k) => cues.births[k] !== undefined && bell(cues.births[k], m, 0.075));
  bell(cues.select, 78, 0.05);
  bell(cues.select + 0.14, 81, 0.05);
  // the knock: a sine falling from 150 to 55 Hz, and a short filtered click
  {
    const i0 = Math.floor(cues.bounce * SR);
    let phase = 0;
    let lp = 0;
    for (let k = 0; k < 0.5 * SR; k++) {
      const t = k / SR;
      const f = 55 + 95 * Math.exp(-t * 30);
      phase += (2 * Math.PI * f) / SR;
      lp += 0.08 * ((rand() * 2 - 1) - lp);
      const s = (Math.sin(phase) * Math.exp(-t / 0.13) * 0.2 + lp * Math.exp(-t / 0.008) * 0.15);
      add(i0 + k, s, s);
    }
  }
  // the logo: a low D swell and a soft bell
  for (const m of [38, 45, 50]) {
    const f = midi(m);
    const i0 = Math.floor(cues.logo * SR);
    for (let k = 0; i0 + k < N; k++) {
      const t = k / SR;
      const s = Math.sin(2 * Math.PI * f * t) * Math.min(1, t / 0.9) * Math.exp(-t / 3.5) * 0.06;
      add(i0 + k, s, s);
    }
  }
  bell(cues.logo + 0.3, 86, 0.04);

  // --- reverb (Freeverb: 8 combs and 4 allpasses per channel) ---------------------------
  const scale = SR / 44100;
  const combs = [1116, 1188, 1277, 1356, 1422, 1491, 1557, 1617];
  const passes = [556, 441, 341, 225];
  const verb = (inp, spread) => {
    const out = new Float32Array(N);
    for (const d of combs) {
      const len = Math.round((d + spread) * scale);
      const buf = new Float32Array(len);
      let idx = 0;
      let store = 0;
      for (let i = 0; i < N; i++) {
        const y = buf[idx];
        store = y * 0.8 + store * 0.2;
        buf[idx] = inp[i] * 0.015 + store * 0.86;
        out[i] += y;
        idx = (idx + 1) % len;
      }
    }
    for (const d of passes) {
      const len = Math.round((d + spread) * scale);
      const buf = new Float32Array(len);
      let idx = 0;
      for (let i = 0; i < N; i++) {
        const b = buf[idx];
        const x = out[i];
        out[i] = -x + b;
        buf[idx] = x + b * 0.5;
        idx = (idx + 1) % len;
      }
    }
    return out;
  };
  const wl = verb(L, 0);
  const wr = verb(R, 23);

  // --- master: dry + wet, fades, soft clip, peak at -1 dBFS ------------------------------
  let peak = 0;
  for (let i = 0; i < N; i++) {
    const t = i / SR;
    const fade = Math.min(1, t / 0.8) * Math.min(1, (cues.duration - t) / 3);
    L[i] = Math.tanh((L[i] + wl[i] * 0.9) * 1.1) * fade;
    R[i] = Math.tanh((R[i] + wr[i] * 0.9) * 1.1) * fade;
    peak = Math.max(peak, Math.abs(L[i]), Math.abs(R[i]));
  }
  const gain = peak > 0 ? 0.89 / peak : 1;

  const data = Buffer.alloc(N * 4);
  for (let i = 0; i < N; i++) {
    data.writeInt16LE(Math.round(Math.max(-1, Math.min(1, L[i] * gain)) * 32767), i * 4);
    data.writeInt16LE(Math.round(Math.max(-1, Math.min(1, R[i] * gain)) * 32767), i * 4 + 2);
  }
  const head = Buffer.alloc(44);
  head.write("RIFF", 0);
  head.writeUInt32LE(36 + data.length, 4);
  head.write("WAVEfmt ", 8);
  head.writeUInt32LE(16, 16);
  head.writeUInt16LE(1, 20); // PCM
  head.writeUInt16LE(2, 22); // stereo
  head.writeUInt32LE(SR, 24);
  head.writeUInt32LE(SR * 4, 28);
  head.writeUInt16LE(4, 32);
  head.writeUInt16LE(16, 34);
  head.write("data", 36);
  head.writeUInt32LE(data.length, 40);
  fs.writeFileSync(file, Buffer.concat([head, data]));
}

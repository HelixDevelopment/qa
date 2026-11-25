// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

// Window-scoped video of the bridge's ONE browser page (HELIX_WEB_VIDEO_DIR).
//
// The bridge starts this script detached at launch; it attaches to the browser over CDP, takes the
// page's own screencast (Page.startScreencast: only that page's pixels, never the host display, unlike
// -record-video which grabs the host with x11grab) and, when the browser goes away or this process is
// told to stop (SIGTERM), assembles the frames into <dir>/<name>.webm with ffmpeg using the real frame
// timestamps, then writes <dir>/<name>.done. Frames are kept in a temp directory and removed afterwards.
//
//   node playwright-bridge-video.js <cdpURL> <dir> <name>
'use strict';
const fs = require('fs');
const os = require('os');
const path = require('path');
const { execFileSync } = require('child_process');
const { chromium } = require('playwright');

const [cdpURL, dir, name] = process.argv.slice(2);
if (!cdpURL || !dir || !name) { process.stderr.write('usage: playwright-bridge-video.js <cdpURL> <dir> <name>\n'); process.exit(2); }

// writeDone records the outcome next to the video; the bridge's close waits for it.
function writeDone(obj) {
  fs.writeFileSync(path.join(dir, name + '.done'), JSON.stringify(obj) + '\n');
}

// assemble turns [{file, t}] (t = seconds) into a webm whose frame durations are the real gaps.
function assemble(frames, outFile) {
  if (frames.length === 0) throw new Error('no frames were captured');
  const list = path.join(path.dirname(frames[0].file), 'list.txt');
  let body = '';
  frames.forEach((f, i) => {
    const next = i + 1 < frames.length ? frames[i + 1].t : f.t + 0.5;
    body += "file '" + f.file.replace(/'/g, "'\\''") + "'\nduration " + Math.max(0.04, next - f.t).toFixed(3) + '\n';
  });
  body += "file '" + frames[frames.length - 1].file.replace(/'/g, "'\\''") + "'\n"; // concat demuxer ignores the last duration
  fs.writeFileSync(list, body);
  execFileSync('ffmpeg', ['-y', '-loglevel', 'error', '-f', 'concat', '-safe', '0', '-i', list,
    '-vf', 'scale=1280:-2', '-c:v', 'libvpx', '-deadline', 'realtime', '-cpu-used', '8', '-threads', '2', '-b:v', '800k',
    '-pix_fmt', 'yuv420p', '-vsync', 'vfr', outFile], { stdio: 'pipe' });
}

(async () => {
  fs.mkdirSync(dir, { recursive: true });
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'helix-video-'));
  const frames = [];
  const MAX_FRAMES = 6000;
  let n = 0;
  let truncated = false;
  let flushPending = () => {};
  let finishing = false;
  async function finish(reason) {
    if (finishing) return;
    finishing = true;
    const out = path.join(dir, name + '.webm');
    try {
      flushPending();
      assemble(frames, out);
      writeDone({ status: truncated ? 'truncated' : 'ok', reason, frames: frames.length, bytes: fs.statSync(out).size });
    } catch (e) {
      writeDone({ status: 'error', reason, frames: frames.length, error: String((e && e.message) || e).slice(0, 300) });
    }
    try { fs.rmSync(tmp, { recursive: true, force: true }); } catch (_) {}
    process.exit(0);
  }
  process.on('SIGTERM', () => { finish('sigterm'); });
  process.on('SIGINT', () => { finish('sigint'); });
  try {
    const browser = await chromium.connectOverCDP(cdpURL);
    browser.on('disconnected', () => finish('browser-gone'));
    const page = browser.contexts()[0].pages()[0];
    const cdp = await browser.contexts()[0].newCDPSession(page);
    // bounded capture: frames closer than 100 ms are held (only the newest is kept) and written once 100 ms have passed, so the
    // settled state of a change burst is never lost; at MAX_FRAMES the recording stops and is reported as truncated, not ok
    let lastT = 0;
    let pending = null;
    function keep(buf, t) {
      if (frames.length >= MAX_FRAMES) { truncated = true; finish('frame-cap'); return; }
      const file = path.join(tmp, 'f' + String(n++).padStart(6, '0') + '.jpg');
      fs.writeFileSync(file, buf);
      frames.push({ file, t });
      lastT = t;
    }
    flushPending = () => { if (pending) { const p = pending; pending = null; keep(p.buf, p.t); } };
    setInterval(() => { if (pending && Date.now() / 1000 - lastT >= 0.1) flushPending(); }, 100);
    cdp.on('Page.screencastFrame', async (ev) => {
      const t = ev.metadata && ev.metadata.timestamp ? ev.metadata.timestamp : Date.now() / 1000;
      const buf = Buffer.from(ev.data, 'base64');
      if (t - lastT < 0.1) pending = { buf, t }; else { pending = null; keep(buf, t); }
      try { await cdp.send('Page.screencastFrameAck', { sessionId: ev.sessionId }); } catch (_) {}
    });
    await cdp.send('Page.startScreencast', { format: 'jpeg', quality: 70, everyNthFrame: 1, maxWidth: 1280, maxHeight: 720 });
    // a screencast only emits on visual change: nudge once so even a static first page has a frame
    await page.evaluate(() => { document.documentElement.style.outline = '0 solid transparent'; }).catch(() => {});
    fs.writeFileSync(path.join(dir, name + '.recording'), String(process.pid) + '\n');
  } catch (e) {
    writeDone({ status: 'error', reason: 'start', frames: 0, error: String((e && e.message) || e).slice(0, 300) });
    process.exit(1);
  }
})();

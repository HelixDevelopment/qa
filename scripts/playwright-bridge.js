#!/usr/bin/env node
// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

// Playwright bridge for HelixQA autonomous QA.
// Accepts a JSON command on stdin, executes it via Playwright
// CDP connection to a headless Chromium, writes result to stdout.
//
// Browser lifecycle:
//   "launch" — starts headless chromium, saves state to file
//   "close"  — kills the browser process
//   All other actions connect to the running browser via CDP.

const { chromium } = require('playwright');
const { execFileSync, spawn } = require('child_process');
const fs = require('fs');
const path = require('path');
const http = require('http');
const net = require('net');
const { tlsArgs } = require('./playwright-bridge-tls');
const { loadCookies } = require('./playwright-bridge-cookies');

const STATE_FILE = path.join(
  process.env.HELIX_OUTPUT_DIR || '/tmp',
  '.helix-browser-state.json'
);

// The CDP port is chosen per launch (an OS-assigned free port) and recorded in the state
// file as cdpURL: a fixed port collided with any other browser or HelixQA session on the host
// and let killExisting() kill a process this bridge never started.
let CDP_PORT = 0;

// freePort asks the OS for a free loopback port.
function freePort() {
  return new Promise((resolve, reject) => {
    const s = net.createServer();
    s.on('error', reject);
    s.listen(0, '127.0.0.1', () => {
      const p = s.address().port;
      s.close(() => resolve(p));
    });
  });
}

// killBrowser kills the browser this bridge spawned (its own process group, since it is
// launched detached), and nothing else.
function killBrowser(pid) {
  if (!pid || pid <= 1) return;
  try { process.kill(-pid, 'SIGKILL'); } catch (_) {
    try { process.kill(pid, 'SIGKILL'); } catch (_) {}
  }
}

// failLaunch aborts a launch that already spawned a browser: the browser is killed and the
// state file removed, so a refused launch never leaves an orphaned Chromium behind.
function failLaunch(proc, message) {
  killBrowser(proc && proc.pid);
  try { fs.unlinkSync(STATE_FILE); } catch (_) {}
  process.stderr.write('error: ' + message + '\n');
  process.exit(1);
}

async function main() {
  const input = fs.readFileSync(0, 'utf-8').trim();
  if (!input) {
    process.stderr.write('error: empty input\n');
    process.exit(1);
  }

  let cmd;
  try {
    cmd = JSON.parse(input);
  } catch (e) {
    process.stderr.write('error: invalid JSON: ' + e.message + '\n');
    process.exit(1);
  }

  switch (cmd.action) {
    case 'launch':
      await handleLaunch(cmd);
      break;
    case 'close':
      await handleClose();
      break;
    default:
      await handleAction(cmd);
      break;
  }
}

// findChromium locates the chromium binary using execFileSync
// to avoid shell injection.
function findChromium() {
  const candidates = [
    'chromium', 'chromium-browser',
    'google-chrome', 'google-chrome-stable',
  ];
  for (const name of candidates) {
    try {
      execFileSync('which', [name], { stdio: 'pipe' });
      return name;
    } catch (_) {}
  }
  return 'chromium';
}

// killExisting silently kills the browser a previous launch recorded in the state file.
// It does not pattern-match other processes: only a pid this bridge recorded is signalled.
function killExisting() {
  if (fs.existsSync(STATE_FILE)) {
    try {
      const state = JSON.parse(fs.readFileSync(STATE_FILE, 'utf-8'));
      killBrowser(state.pid);
    } catch (_) {}
    try { fs.unlinkSync(STATE_FILE); } catch (_) {}
  }
}

// waitForCDP polls the CDP endpoint until it responds.
function waitForCDP(url, retries) {
  return new Promise((resolve, reject) => {
    let attempt = 0;
    function tryConnect() {
      http.get(url + '/json/version', (res) => {
        let data = '';
        res.on('data', chunk => { data += chunk; });
        res.on('end', () => resolve(true));
      }).on('error', () => {
        attempt++;
        if (attempt >= retries) {
          reject(new Error('CDP not ready after ' + retries + ' attempts'));
        } else {
          setTimeout(tryConnect, 500);
        }
      });
    }
    tryConnect();
  });
}

// apiLogin authenticates via the API and returns the session token.
// This bypasses the need for coordinate-based form interaction.
function apiLogin(baseUrl, username, password) {
  return new Promise((resolve, reject) => {
    const url = new URL('/api/v1/auth/login', baseUrl);
    const body = JSON.stringify({ username, password });
    const proto = url.protocol === 'https:' ? require('https') : http;
    const reqOpts = {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Content-Length': Buffer.byteLength(body),
      },
    };
    if (url.protocol === 'https:' && process.env.HELIX_WEB_CA_FILE) {
      try { reqOpts.ca = fs.readFileSync(process.env.HELIX_WEB_CA_FILE); } catch (_) { /* reported at launch */ }
    }
    const req = proto.request(url, reqOpts, (res) => {
      let data = '';
      res.on('data', chunk => { data += chunk; });
      res.on('end', () => {
        try {
          const parsed = JSON.parse(data);
          if (parsed.session_token) {
            resolve(parsed.session_token);
          } else {
            reject(new Error('No session_token in response'));
          }
        } catch (e) {
          reject(new Error('API login parse error: ' + e.message));
        }
      });
    });
    req.on('error', reject);
    req.write(body);
    req.end();
  });
}

async function handleLaunch(cmd) {
  let videoName = '';
  // Kill any existing browser silently.
  killExisting();

  // Explicit TLS trust options (HELIX_WEB_CA_FILE / HELIX_WEB_INSECURE_TLS);
  // an invalid or refused option aborts the launch instead of degrading.
  let tlsExtra;
  try {
    tlsExtra = await tlsArgs(process.env, cmd.url || '');
  } catch (e) {
    process.stderr.write('error: ' + e.message + '\n');
    process.exit(1);
  }

  // Operator-supplied session cookies (HELIX_WEB_COOKIE_FILE); validated BEFORE
  // the browser starts, an invalid file aborts the launch.
  let sessionCookies;
  try {
    sessionCookies = loadCookies(process.env, cmd.url || '');
  } catch (e) {
    process.stderr.write('error: ' + e.message + '\n');
    process.exit(1);
  }

  const chromiumPath = findChromium();
  CDP_PORT = await freePort();
  const cdpURL = 'http://127.0.0.1:' + CDP_PORT;

  // Launch headless chromium with CDP.
  const proc = spawn(chromiumPath, [
    '--headless=new',
    '--no-sandbox',
    '--disable-gpu',
    '--disable-dev-shm-usage',
    '--remote-debugging-port=' + CDP_PORT,
    '--window-size=1920,1080',
    '--hide-scrollbars',
    ...tlsExtra,
    cmd.url || 'about:blank',
  ], {
    stdio: 'ignore',
    detached: true,
  });
  proc.unref();

  // Record the browser immediately so any later failure (or the next launch) can find and kill it.
  fs.writeFileSync(STATE_FILE, JSON.stringify({ pid: proc.pid, cdpURL, url: cmd.url }));

  // Wait for CDP to be ready.
  try {
    await waitForCDP(cdpURL, 40);
  } catch (e) {
    failLaunch(proc, e.message);
  }

  if (sessionCookies) {
    try {
      const b = await chromium.connectOverCDP(cdpURL);
      const ctx = b.contexts()[0];
      const pg = ctx.pages()[0];
      if (!pg) throw new Error('no page');
      // The startup tab does not see cookies added through ctx.addCookies
      // (measured: document.cookie stays empty); the tab's own CDP session does.
      const cdp = await ctx.newCDPSession(pg);
      await cdp.send('Network.setCookies', { cookies: sessionCookies });
      await pg.goto(cmd.url, { waitUntil: 'load', timeout: 30000 });
      process.stderr.write('session cookies installed: ' + sessionCookies.length + '\n');
    } catch (e) {
      failLaunch(proc, 'HELIX_WEB_COOKIE_FILE: could not install the cookies in the browser (' + (e && e.name || 'Error') + ')');
    }
  }

  // Window-scoped video of this one page (HELIX_WEB_VIDEO_DIR): a detached recorder takes the page's own
  // screencast over CDP; close waits for it to write <name>.done. A refused start aborts the launch.
  let videoPid = 0;
  const videoDir = process.env.HELIX_WEB_VIDEO_DIR;
  if (videoDir) {
    try {
      const name = 'web-' + new Date().toISOString().replace(/[:.]/g, '-');
      const rec = spawn(process.execPath, [path.join(__dirname, 'playwright-bridge-video.js'), cdpURL, videoDir, name], {
        stdio: 'ignore', detached: true, env: process.env });
      rec.unref();
      videoPid = rec.pid;
      for (let i = 0; i < 100 && !fs.existsSync(path.join(videoDir, name + '.recording')); i++) {
        if (fs.existsSync(path.join(videoDir, name + '.done'))) throw new Error('recorder refused to start');
        await new Promise(r => setTimeout(r, 100));
      }
      if (!fs.existsSync(path.join(videoDir, name + '.recording'))) throw new Error('recorder did not start');
      videoName = name;
    } catch (e) {
      failLaunch(proc, 'HELIX_WEB_VIDEO_DIR: could not start the page recorder (' + (e && e.message || e) + ')');
    }
  }

  // Pre-login: authenticate via API and inject token into browser.
  // This bypasses coordinate-based form interaction which is unreliable
  // in headless mode. The React app reads auth_token from localStorage.
  const apiBase = cmd.apiUrl || cmd.url?.replace(':3000', ':8080') || 'http://localhost:8080';
  // SECURITY: no built-in credential. The login is sent only when the operator
  // supplied one (cmd.username/password or ADMIN_USERNAME/ADMIN_PASSWORD);
  // otherwise nothing is posted to the target.
  const username = cmd.username || process.env.ADMIN_USERNAME || '';
  const password = cmd.password || process.env.ADMIN_PASSWORD || '';

  if (!username || !password) {
    process.stderr.write('pre-login skipped: no credentials supplied (ADMIN_USERNAME / ADMIN_PASSWORD)\n');
  } else try {
    const token = await apiLogin(apiBase, username, password);
    // Connect to browser and inject the token.
    const browser = await chromium.connectOverCDP(cdpURL);
    const contexts = browser.contexts();
    if (contexts.length > 0) {
      const pages = contexts[0].pages();
      if (pages.length > 0) {
        const page = pages[0];
        // Wait for page to load, then inject auth token.
        await page.waitForLoadState('load').catch(() => {});
        await page.evaluate(([t, u]) => {
          localStorage.setItem('auth_token', t);
          localStorage.setItem('user', JSON.stringify({
            id: 1, username: u, role: { name: 'Admin' }
          }));
        }, [token, username]);
        // Navigate to dashboard (reload with token in place).
        await page.goto(cmd.url || 'http://localhost:3000', {
          waitUntil: 'domcontentloaded',
          timeout: 10000,
        }).catch(() => {});
        // Brief wait for React to hydrate with the auth token.
        await page.waitForTimeout(2000);
        process.stderr.write('pre-login: token injected, navigated to dashboard\n');
      }
    }
    // Don't call browser.disconnect() — it hangs on some CDP
    // connections. The browser stays alive via the detached spawn.
  } catch (e) {
    // Pre-login failed — browser still launches, LLM will see login page.
    process.stderr.write('pre-login warning: ' + e.message + '\n');
  }

  // Save state — viewport setup happens on first action.
  fs.writeFileSync(STATE_FILE, JSON.stringify({
    pid: proc.pid,
    cdpURL,
    url: cmd.url,
    videoPid,
    videoDir: videoDir || '',
    videoName,
  }));

  process.stdout.write(JSON.stringify({
    status: 'launched',
    pid: proc.pid,
  }));

  // Force exit — CDP connections keep the node process alive.
  // The browser runs as a detached process independently.
  setTimeout(() => process.exit(0), 500);
}

async function handleClose() {
  if (!fs.existsSync(STATE_FILE)) {
    process.stdout.write(JSON.stringify({ status: 'no_browser' }));
    return;
  }
  const state = JSON.parse(fs.readFileSync(STATE_FILE, 'utf-8'));
  // Stop the page recorder FIRST (it finalizes the video from the frames it holds), wait for its .done, then the browser.
  let video = null;
  if (state.videoPid && state.videoDir && state.videoName) {
    const done = path.join(state.videoDir, state.videoName + '.done');
    // signal the recorder only while it is provably still ours: no .done yet AND the pid's command line is the recorder with our name
    // (it exits by itself when the browser goes away, and its pid may then belong to another process)
    let ours = false;
    if (!fs.existsSync(done)) {
      try { ours = fs.readFileSync('/proc/' + state.videoPid + '/cmdline', 'utf8').split('\0').some(a => a === state.videoName); } catch (_) {}
    }
    if (ours) { try { process.kill(state.videoPid, 'SIGTERM'); } catch (_) {} }
    for (let i = 0; i < 6000 && !fs.existsSync(done); i++) await new Promise(r => setTimeout(r, 100));
    try { video = JSON.parse(fs.readFileSync(done, 'utf-8')); } catch (_) { video = { status: 'error', error: 'recorder wrote no .done' }; }
  }
  try {
    process.kill(state.pid, 'SIGTERM');
  } catch (_) {}
  try {
    fs.unlinkSync(STATE_FILE);
  } catch (_) {}
  process.stdout.write(JSON.stringify(video ? { status: 'closed', video } : { status: 'closed' }));
}

async function handleAction(cmd) {
  if (!fs.existsSync(STATE_FILE)) {
    process.stderr.write('error: no browser — call launch first\n');
    process.exit(1);
  }

  const state = JSON.parse(fs.readFileSync(STATE_FILE, 'utf-8'));
  let browser;
  try {
    browser = await chromium.connectOverCDP(state.cdpURL);
  } catch (e) {
    process.stderr.write('error: connect: ' + e.message + '\n');
    process.exit(1);
  }

  const contexts = browser.contexts();
  if (contexts.length === 0) {
    if (typeof browser.disconnect === 'function') {
      browser.disconnect();
    }
    process.stderr.write('error: no browser context\n');
    process.exit(1);
  }
  const pages = contexts[0].pages();
  if (pages.length === 0) {
    if (typeof browser.disconnect === 'function') {
      browser.disconnect();
    }
    process.stderr.write('error: no page\n');
    process.exit(1);
  }
  const page = pages[0];

  try {
    switch (cmd.action) {
      case 'screenshot': {
        // Wait for page to be fully loaded and stable before screenshot
        await page.waitForLoadState('networkidle', { timeout: 5000 }).catch(() => {});
        await page.waitForTimeout(500); // Extra delay for any animations/rendering
        
        const buf = await page.screenshot({
          type: 'png',
          fullPage: false,
        });
        
        // Verify screenshot is not blank (has meaningful content)
        if (buf.length < 1000) {
          // Too small - likely blank or error
          process.stderr.write('warning: screenshot appears blank (' + buf.length + ' bytes)\n');
        }
        
        // Check for all-white screenshot by sampling pixels
        const hasContent = await page.evaluate(() => {
          const canvas = document.createElement('canvas');
          canvas.width = 100;
          canvas.height = 100;
          const ctx = canvas.getContext('2d');
          try {
            ctx.drawWindow(window, 0, 0, 100, 100, 'rgb(255,255,255)');
            const data = ctx.getImageData(0, 0, 100, 100).data;
            let total = 0;
            for (let i = 0; i < data.length; i += 4) {
              total += data[i] + data[i+1] + data[i+2];
            }
            return total < (255 * 3 * 100 * 100 * 0.99); // Not all white
          } catch (e) {
            return true; // Can't check, assume content exists
          }
        }).catch(() => true);
        
        if (!hasContent) {
          process.stderr.write('warning: screenshot detected as all-white/blank\n');
        }
        
        process.stdout.write(buf);
        break;
      }
      case 'click': {
        await page.mouse.click(cmd.x, cmd.y);
        ok();
        break;
      }
      case 'type': {
        await page.keyboard.type(cmd.text, { delay: 30 });
        ok();
        break;
      }
      case 'scroll': {
        let dx = 0, dy = 0;
        const amt = cmd.amount || 300;
        if (cmd.direction === 'up')    dy = -amt;
        if (cmd.direction === 'down')  dy = amt;
        if (cmd.direction === 'left')  dx = -amt;
        if (cmd.direction === 'right') dx = amt;
        await page.mouse.wheel(dx, dy);
        ok();
        break;
      }
      case 'key': {
        await page.keyboard.press(cmd.key);
        ok();
        break;
      }
      case 'back': {
        await page.goBack({ timeout: 10000 }).catch(() => {});
        ok();
        break;
      }
      case 'navigate': {
        await page.goto(cmd.url, {
          waitUntil: 'load',
          timeout: 30000,
        });
        ok();
        break;
      }
      case 'longpress': {
        await page.mouse.move(cmd.x, cmd.y);
        await page.mouse.down();
        await page.waitForTimeout(1000);
        await page.mouse.up();
        ok();
        break;
      }
      case 'swipe': {
        await page.mouse.move(cmd.fromX, cmd.fromY);
        await page.mouse.down();
        await page.mouse.move(cmd.toX, cmd.toY, { steps: 10 });
        await page.mouse.up();
        ok();
        break;
      }
      default:
        process.stderr.write('error: unknown action: ' + cmd.action + '\n');
        process.exit(1);
    }
  } finally {
    // Force exit — disconnect() hangs on some CDP
    // connections. The browser process stays alive
    // independently via the detached spawn.
    setTimeout(() => process.exit(0), 500);
  }
}

function ok() {
  process.stdout.write(JSON.stringify({ status: 'ok' }));
}

main().catch(e => {
  process.stderr.write('error: ' + e.message + '\n');
  process.exit(1);
});

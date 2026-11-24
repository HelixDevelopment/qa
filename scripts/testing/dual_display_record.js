const { chromium } = require('playwright-core');
const fs = require('fs');
const { execSync } = require('child_process');

const [,, command, testName, shotDir] = process.argv;
const BASE = 'http://127.0.0.1:4200';
const PID_FILE = '/tmp/dual_display_record.pid';

async function startRecording() {
    const SHOT_DIR = shotDir || `/tmp/__test_recording/${testName}_shots`;
    const VIDEO = `/tmp/__test_recording/${testName}_primary.mp4`;
    fs.mkdirSync(SHOT_DIR, { recursive: true });

    const browser = await chromium.launch({
        executablePath: '/usr/bin/chromium-browser',
        headless: true,
        args: ['--no-sandbox', '--disable-gpu', '--headless=new']
    });
    const page = await browser.newPage({ viewport: { width: 1920, height: 1080 } });

    // Authenticate with operator-supplied credentials only (no built-in default).
    const LOGIN_USER = process.env.HELIX_TEST_USER || '';
    const LOGIN_PASS = process.env.HELIX_TEST_PASSWORD || '';
    if (!LOGIN_USER || !LOGIN_PASS) {
        console.error('error: set HELIX_TEST_USER and HELIX_TEST_PASSWORD (no default credentials exist)');
        await browser.close();
        process.exit(2);
    }
    await page.goto(BASE + '/login', { waitUntil: 'domcontentloaded', timeout: 15000 });
    const token = await page.evaluate(async ([u, p]) => {
        const r = await fetch('/api/v1/auth/login', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ email: u, password: p })
        });
        const d = await r.json();
        return d.access_token || d.token || '';
    });
    await page.evaluate((t) => { localStorage.setItem('helix_token', t); }, token);

    const pages = ['login','dashboard','products','orders','customers','merchant/profile','merchant/settings','payouts','webhooks','providers','subscription','reports'];
    for (let i = 0; i < pages.length; i++) {
        try {
            await page.goto(BASE + '/' + pages[i], { waitUntil: 'networkidle', timeout: 15000 });
            await page.waitForTimeout(2000);
            await page.screenshot({ path: `${SHOT_DIR}/shot_${String(i+1).padStart(4,'0')}.png` });
        } catch (e) {
            try { await page.screenshot({ path: `${SHOT_DIR}/shot_${String(i+1).padStart(4,'0')}.png` }); } catch(e2) {}
        }
    }
    await browser.close();

    execSync(`ffmpeg -y -framerate 1 -pattern_type glob -i "${SHOT_DIR}/shot_*.png" -c:v libx264 -preset ultrafast -crf 28 -pix_fmt yuv420p "${VIDEO}"`, { stdio: 'ignore', timeout: 30000 });
    fs.rmSync(PID_FILE);
    process.exit(0);
}

function stopRecording() {
    if (fs.existsSync(PID_FILE)) {
        const pid = parseInt(fs.readFileSync(PID_FILE, 'utf8').trim());
        try { process.kill(pid); } catch(e) {}
    }
    const VIDEO = `/tmp/__test_recording/${testName}_primary.mp4`;
    for (let i = 0; i < 30; i++) {
        if (fs.existsSync(VIDEO)) {
            console.log(fs.statSync(VIDEO).size);
            break;
        }
    }
    process.exit(0);
}

if (command === 'start') startRecording().catch(() => process.exit(1));
else if (command === 'stop') stopRecording();
else { console.log('Usage: node record.js {start|stop} <testName>'); process.exit(1); }

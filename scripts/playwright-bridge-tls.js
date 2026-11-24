// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

// TLS trust options for the HelixQA Playwright bridge.
//
// The bridge launches a plain Chromium that trusts only the system store, so a
// TLS-only target signed by a PRIVATE CA renders as a certificate interstitial
// and every screenshot is useless. Two EXPLICIT, opt-in options exist; with
// neither set the browser keeps its default (strict) behaviour:
//
//   HELIX_WEB_CA_FILE=<pem>      trust the CA(s) in this PEM bundle. Chromium has
//                                no flag that loads a CA file and a server
//                                normally sends only its leaf, so the bridge
//                                itself opens a TLS connection to the target
//                                host:port and VERIFIES the presented chain
//                                against that bundle (hostname included); only
//                                when that verification succeeds is the leaf's
//                                SPKI sha256 passed to
//                                --ignore-certificate-errors-spki-list. A leaf
//                                the bundle does not vouch for, or an unreadable
//                                or certificate-free file, is an ERROR, never a
//                                silent fall-back to "no trust". Only the
//                                verified origin is covered; a redirect to
//                                another host is not.
//   HELIX_WEB_INSECURE_TLS=1     ignore ALL certificate errors, but ONLY when
//                                the target host is loopback, localhost, a
//                                *.local / *.localhost name or a private-range
//                                address (RFC 1918, link-local, ULA). Any other
//                                host is REFUSED.

'use strict';
const fs = require('fs');
const crypto = require('crypto');

function isPrivateHost(host) {
  const h = String(host || '').toLowerCase().replace(/^\[|\]$/g, '');
  if (h === 'localhost' || h.endsWith('.localhost') || h.endsWith('.local')) return true;
  if (h === '::1') return true;
  if (/^f[cd][0-9a-f]{2}:/.test(h) || /^fe[89ab][0-9a-f]:/.test(h)) return true; // ULA, link-local
  const m = h.match(/^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/);
  if (!m) return false;
  const [a, b, c, d] = m.slice(1).map(Number);
  if ([a, b, c, d].some((n) => n > 255)) return false;
  return a === 127 || a === 10 || (a === 172 && b >= 16 && b <= 31) ||
    (a === 192 && b === 168) || (a === 169 && b === 254);
}

function spkiHashes(pemText) {
  const blocks = pemText.match(/-----BEGIN CERTIFICATE-----[\s\S]+?-----END CERTIFICATE-----/g) || [];
  return blocks.map((b) => {
    const spki = new crypto.X509Certificate(b).publicKey.export({ type: 'spki', format: 'der' });
    return crypto.createHash('sha256').update(spki).digest('base64');
  });
}

// tlsArgs returns the extra Chromium arguments for the given environment and
// target URL, or throws an Error naming the offending variable.
function verifiedLeafSpki(targetUrl, caPem) {
  const tls = require('tls');
  return new Promise((resolve, reject) => {
    let u;
    try { u = new URL(targetUrl); } catch (e) { return reject(new Error('HELIX_WEB_CA_FILE: invalid target URL')); }
    if (u.protocol !== 'https:') return resolve(null); // plain http needs no trust
    const host = u.hostname.replace(/^\[|\]$/g, '');
    const opts = { host, port: Number(u.port) || 443, ca: caPem, rejectUnauthorized: true };
    if (require('net').isIP(host) === 0) opts.servername = host; // SNI is not permitted for an IP literal
    const sock = tls.connect(opts, () => {
      try {
        const raw = sock.getPeerCertificate(true).raw;
        const spki = new crypto.X509Certificate(raw).publicKey.export({ type: 'spki', format: 'der' });
        sock.end();
        resolve(crypto.createHash('sha256').update(spki).digest('base64'));
      } catch (e) { sock.destroy(); reject(e); }
    });
    sock.setTimeout(15000, () => { sock.destroy(); reject(new Error('timeout verifying ' + host)); });
    sock.on('error', (e) => reject(new Error('the certificate presented by ' + host + ' is not vouched for by the CA bundle: ' + e.message)));
  });
}

async function tlsArgs(env, targetUrl) {
  const args = [];
  const caFile = env.HELIX_WEB_CA_FILE;
  if (caFile) {
    let text;
    try { text = fs.readFileSync(caFile, 'utf8'); } catch (e) {
      throw new Error('HELIX_WEB_CA_FILE: cannot read ' + caFile + ': ' + e.message);
    }
    let hashes;
    try { hashes = spkiHashes(text); } catch (e) {
      throw new Error('HELIX_WEB_CA_FILE: unparseable certificate in ' + caFile + ': ' + e.message);
    }
    if (hashes.length === 0) throw new Error('HELIX_WEB_CA_FILE: no PEM certificate found in ' + caFile);
    let leaf;
    try { leaf = await verifiedLeafSpki(targetUrl, text); } catch (e) {
      throw new Error('HELIX_WEB_CA_FILE: ' + e.message);
    }
    if (leaf) args.push('--ignore-certificate-errors-spki-list=' + leaf);
  }
  if (env.HELIX_WEB_INSECURE_TLS === '1' || env.HELIX_WEB_INSECURE_TLS === 'true') {
    let host = '';
    try { host = new URL(targetUrl).hostname; } catch (_) { /* refused below */ }
    if (!isPrivateHost(host)) {
      throw new Error('HELIX_WEB_INSECURE_TLS is limited to localhost / loopback / private-range hosts; refusing for host "' +
        host + '" (use HELIX_WEB_CA_FILE to trust a specific CA instead)');
    }
    args.push('--ignore-certificate-errors');
  }
  return args;
}

module.exports = { tlsArgs, isPrivateHost, spkiHashes };

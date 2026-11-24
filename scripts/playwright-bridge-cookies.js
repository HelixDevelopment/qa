// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

// Operator-supplied session for the HelixQA Playwright bridge.
//
//   HELIX_WEB_COOKIE_FILE=<json>   a JSON array of cookies,
//     [{"name":"sid","value":"...","path":"/","httpOnly":true,"secure":true,"sameSite":"Lax"}]
//   The cookies are bound to the TARGET URL's origin (never a domain taken from
//   the file: a "domain" or "url" key is refused), so they cannot be handed to
//   another site. The file is a credential: it must not be readable by group or
//   other (mode 0600), and no cookie value is ever written to an error or log.
//   Any problem aborts the launch; there is no silent fall-back to "anonymous".
'use strict';
const fs = require('fs');

const ALLOWED = new Set(['name', 'value', 'path', 'httpOnly', 'secure', 'sameSite', 'expires']);

// loadCookies returns the validated cookie list for the target URL, or null
// when the variable is unset. It throws an Error naming HELIX_WEB_COOKIE_FILE.
function loadCookies(env, targetUrl) {
  const file = env.HELIX_WEB_COOKIE_FILE;
  if (!file) return null;
  const fail = (m) => { throw new Error('HELIX_WEB_COOKIE_FILE: ' + m); };
  let st;
  try { st = fs.statSync(file); } catch (e) { fail('cannot read ' + file + ': ' + e.code); }
  if (!st.isFile()) fail(file + ' is not a regular file');
  if ((st.mode & 0o077) !== 0) fail(file + ' is readable by group/other; chmod 600 it (it holds a session credential)');
  let list;
  try { list = JSON.parse(fs.readFileSync(file, 'utf8')); } catch (_) { fail('not valid JSON'); }
  if (!Array.isArray(list) || list.length === 0) fail('must be a non-empty JSON array of cookies');
  let u;
  try { u = new URL(targetUrl); } catch (_) { fail('the target URL is invalid'); }
  return list.map((c, i) => {
    if (!c || typeof c !== 'object' || Array.isArray(c)) fail('entry ' + i + ' is not an object');
    for (const k of Object.keys(c)) {
      if (!ALLOWED.has(k)) fail('entry ' + i + ' has unsupported key "' + k + '" (cookies are bound to the target origin; domain/url are refused)');
    }
    if (typeof c.name !== 'string' || c.name === '') fail('entry ' + i + ' has no name');
    if (typeof c.value !== 'string' || c.value === '') fail('entry ' + i + ' has no value');
    // Playwright takes either url or path, never both: scope by url only.
    const out = Object.assign({}, c);
    delete out.path;
    out.url = u.origin + (typeof c.path === 'string' && c.path.startsWith('/') ? c.path : '/');
    return out;
  });
}

module.exports = { loadCookies };

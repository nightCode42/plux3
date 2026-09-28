// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// NFR-020: manifest endpoint throughput. Every simulated device syncs
// once, then checks for updates at a constant arrival rate with the ETag
// it holds — the request devices make on every app start when nothing
// changed. Each device sends its own X-Forwarded-For through a trusted
// proxy, so the per-address and per-device rate limits (SRV-065) apply
// per simulated device, as they would in the field.
//
//   k6 run -e BASE_URL=http://localhost:8080 -e APP_ID=<id> -e ENVIRONMENT=staging test/load/manifest.js
//
// RATE (default 5000/s), DURATION (60s) and DEVICES (6000; each may make 120 calls a minute) tune the run.

import http from 'k6/http';
import { check, fail } from 'k6';

const base = __ENV.BASE_URL || 'http://localhost:8080';
const rate = Number(__ENV.RATE || 5000);
const devices = Number(__ENV.DEVICES || 6000);

export const options = {
  setupTimeout: '10m',
  scenarios: {
    manifest: {
      executor: 'constant-arrival-rate',
      rate,
      timeUnit: '1s',
      duration: __ENV.DURATION || '60s',
      preAllocatedVUs: 600,
      maxVUs: 2000,
    },
  },
  thresholds: {
    'http_req_duration{name:GetManifest}': ['p(99)<50'],
    'http_req_failed{name:GetManifest}': ['rate<0.001'],
    'checks{name:GetManifest}': ['rate>0.999'],
  },
  summaryTrendStats: ['avg', 'p(50)', 'p(95)', 'p(99)', 'max'],
};

function headers(token, address) {
  const h = { 'Content-Type': 'application/json', 'X-Forwarded-For': address };
  if (token) h.Authorization = `Bearer ${token}`;
  return h;
}

function call(procedure, body, token, address) {
  return http.post(`${base}/plux.v1.${procedure}`, JSON.stringify(body), { headers: headers(token, address), tags: { name: procedure.split('/')[1] } });
}

// address gives each device its own address in 10.0.0.0/8.
function address(i) {
  return `10.${(i >> 16) & 255}.${(i >> 8) & 255}.${i & 255}`;
}

export function setup() {
  const out = [];
  for (let i = 0; i < devices; i++) {
    const ip = address(i + 1);
    const reg = call('DeviceService/RegisterDevice', { appId: __ENV.APP_ID, environment: __ENV.ENVIRONMENT || 'staging', platform: 'android', runtimeVersion: '1.0.0' }, '', ip);
    if (reg.status !== 200) fail(`RegisterDevice: ${reg.status} ${reg.body}`);
    const d = reg.json();
    const tok = call('TokenService/IssueDeviceToken', { deviceId: d.device.id, deviceSecret: d.deviceSecret }, '', ip);
    if (tok.status !== 200) fail(`IssueDeviceToken: ${tok.status} ${tok.body}`);
    const token = tok.json().accessToken;
    const first = call('ManifestService/GetManifest', {}, token, ip);
    if (first.status !== 200) fail(`GetManifest: ${first.status} ${first.body}`);
    const m = first.json().manifest;
    const installed = [{ key: '', sha256: m.appBundle.sha256 }].concat((m.plugins || []).map((p) => ({ key: p.key, sha256: p.bundle.sha256 })));
    const synced = call('ManifestService/GetManifest', { installedSequence: m.releaseSequence, installed }, token, ip).json();
    // Serialised once, so the load generator spends its time sending.
    out.push({ headers: headers(token, ip), body: JSON.stringify({ installedSequence: m.releaseSequence, installed, ifNoneMatch: synced.etag }) });
  }
  return out;
}

const url = `${base}/plux.v1.ManifestService/GetManifest`;
const params = { tags: { name: 'GetManifest' } };

export default function (all) {
  const d = all[Math.floor(Math.random() * all.length)];
  const res = http.post(url, d.body, Object.assign({ headers: d.headers }, params));
  check(res, { 'not modified': (r) => r.status === 200 && r.body.indexOf('"notModified":true') >= 0 }, { name: 'GetManifest' });
}

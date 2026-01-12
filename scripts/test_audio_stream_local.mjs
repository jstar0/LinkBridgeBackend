import fs from 'node:fs';
import { performance } from 'node:perf_hooks';

const BASE_URL = process.env.BASE_URL || 'http://127.0.0.1:18082';
const WS_URL = process.env.WS_URL || 'ws://127.0.0.1:18082';

const SAMPLE_RATE = Number(process.env.SAMPLE_RATE || 16000);
const DURATION_SEC = Number(process.env.DURATION_SEC || 10);
const FRAME_BYTES = Number(process.env.FRAME_BYTES || 5120); // 5KB ~= 160ms @ 16kHz 16-bit mono
const OUTPUT_DIR = process.env.OUTPUT_DIR || '';
const TEST_VIDEO = (process.env.TEST_VIDEO || '1') !== '0';
const VIDEO_FRAMES = Number(process.env.VIDEO_FRAMES || 30);
const VIDEO_INTERVAL_MS = Number(process.env.VIDEO_INTERVAL_MS || 80);

function sleep(ms) {
  return new Promise((r) => setTimeout(r, ms));
}

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

function makeSuffix() {
  const t = Math.floor(Date.now() / 1000).toString(36);
  const r = Math.floor(Math.random() * 1e6).toString(36);
  return `${t}_${r}`.replace(/[^a-zA-Z0-9_]/g, '_').slice(0, 12);
}

async function http(method, path, body, token) {
  const url = new URL(path, BASE_URL);
  const headers = {};
  if (token) headers.Authorization = `Bearer ${token}`;
  const init = { method, headers };
  if (body !== undefined && body !== null) {
    headers['Content-Type'] = 'application/json';
    init.body = JSON.stringify(body);
  }
  const res = await fetch(url, init);
  const text = await res.text();
  let parsed;
  try {
    parsed = text ? JSON.parse(text) : {};
  } catch {
    parsed = { raw: text };
  }
  if (!res.ok) throw new Error(`HTTP ${res.status} ${method} ${path}: ${text}`);
  return parsed;
}

function makeWavHeader({ numChannels, sampleRate, bitsPerSample, dataSize }) {
  const blockAlign = (numChannels * bitsPerSample) / 8;
  const byteRate = sampleRate * blockAlign;
  const buf = Buffer.alloc(44);
  buf.write('RIFF', 0, 4, 'ascii');
  buf.writeUInt32LE(36 + dataSize, 4);
  buf.write('WAVE', 8, 4, 'ascii');
  buf.write('fmt ', 12, 4, 'ascii');
  buf.writeUInt32LE(16, 16); // PCM fmt chunk size
  buf.writeUInt16LE(1, 20); // PCM format
  buf.writeUInt16LE(numChannels, 22);
  buf.writeUInt32LE(sampleRate, 24);
  buf.writeUInt32LE(byteRate, 28);
  buf.writeUInt16LE(blockAlign, 32);
  buf.writeUInt16LE(bitsPerSample, 34);
  buf.write('data', 36, 4, 'ascii');
  buf.writeUInt32LE(dataSize, 40);
  return buf;
}

function generateSinePcm16le({ sampleRate, durationSec, hz = 440, amplitude = 0.25 }) {
  const totalSamples = Math.max(1, Math.floor(sampleRate * durationSec));
  const out = Buffer.alloc(totalSamples * 2);
  for (let i = 0; i < totalSamples; i++) {
    const t = i / sampleRate;
    const s = Math.sin(2 * Math.PI * hz * t);
    const v = Math.max(-1, Math.min(1, s * amplitude));
    const sample = Math.round(v * 32767);
    out.writeInt16LE(sample, i * 2);
  }
  return out;
}

async function openWS(token, onMessage) {
  return await new Promise((resolve, reject) => {
    const ws = new WebSocket(`${WS_URL}/v1/ws?token=${encodeURIComponent(token)}`);
    const timeout = setTimeout(() => reject(new Error('ws open timeout')), 8000);
    ws.onopen = () => {
      clearTimeout(timeout);
      resolve(ws);
    };
    ws.onerror = (err) => {
      clearTimeout(timeout);
      reject(err);
    };
    ws.onmessage = (evt) => {
      const raw = typeof evt.data === 'string' ? evt.data : evt.data.toString();
      try {
        onMessage(JSON.parse(raw));
      } catch {
        // ignore non-json
      }
    };
  });
}

function summarizeTimings(values) {
  const v = values.filter((x) => Number.isFinite(x)).sort((a, b) => a - b);
  if (!v.length) return null;
  const sum = v.reduce((a, b) => a + b, 0);
  const p = (q) => v[Math.floor((v.length - 1) * q)];
  return {
    count: v.length,
    min: v[0],
    p50: p(0.5),
    p90: p(0.9),
    p99: p(0.99),
    max: v[v.length - 1],
    avg: sum / v.length,
  };
}

async function main() {
  const suffix = makeSuffix();
  const password = 'TestP@ss123!';

  console.log(`BASE_URL=${BASE_URL}`);
  console.log(`WS_URL=${WS_URL}`);
  console.log(`SAMPLE_RATE=${SAMPLE_RATE} DURATION_SEC=${DURATION_SEC} FRAME_BYTES=${FRAME_BYTES}`);

  const u1 = await http('POST', '/v1/auth/register', {
    username: `a_${suffix}`,
    password,
    displayName: `A_${suffix}`.slice(0, 20),
  });
  const u2 = await http('POST', '/v1/auth/register', {
    username: `b_${suffix}`,
    password,
    displayName: `B_${suffix}`.slice(0, 20),
  });
  const token1 = u1.token;
  const token2 = u2.token;
  const id2 = u2.user?.id;
  assert(token1 && token2 && id2, 'missing tokens or user ids');

  await http('POST', '/v1/sessions', { peerUserId: id2 }, token1);
  const call = await http('POST', '/v1/calls', { calleeUserId: id2, mediaType: 'voice' }, token1);
  const callId = call.call?.id;
  assert(callId, 'missing callId');
  await http('POST', `/v1/calls/${encodeURIComponent(callId)}/accept`, {}, token2);

  const received = new Map(); // seq -> { sentAtMs, recvAtMs, data }
  const receivedVideo = new Map(); // seq -> { sentAtMs, recvAtMs, data }
  const lagsMs = [];
  const gapsMs = [];
  let lastRecvAtMs = 0;

  const ws2 = await openWS(token2, (env) => {
    const p = env?.payload;
    if (!p || p.callId !== callId) return;
    const seq = Number(p.seq || 0);
    const sentAtMs = Number(p.sentAtMs || 0);
    const recvAtMs = Date.now();
    const data = p.data;
    if (!seq || !data) return;

    if (env?.type === 'audio.frame') {
      if (!received.has(seq)) {
        received.set(seq, { sentAtMs, recvAtMs, data });
      }

      if (sentAtMs) lagsMs.push(recvAtMs - sentAtMs);
      if (lastRecvAtMs) gapsMs.push(recvAtMs - lastRecvAtMs);
      lastRecvAtMs = recvAtMs;
    }

    if (env?.type === 'video.frame') {
      if (!receivedVideo.has(seq)) {
        receivedVideo.set(seq, { sentAtMs, recvAtMs, data });
      }
    }
  });

  const ws1 = await openWS(token1, () => {});

  const pcm = generateSinePcm16le({ sampleRate: SAMPLE_RATE, durationSec: DURATION_SEC });
  const totalFrames = Math.ceil(pcm.length / FRAME_BYTES);
  const frameIntervalMs = (FRAME_BYTES / (SAMPLE_RATE * 2)) * 1000;

  console.log(`callId=${callId} totalFrames=${totalFrames} frameIntervalMs≈${frameIntervalMs.toFixed(1)}`);

  const sendStart = performance.now();
  for (let i = 0; i < totalFrames; i++) {
    const start = i * FRAME_BYTES;
    const end = Math.min(pcm.length, start + FRAME_BYTES);
    const chunk = pcm.subarray(start, end);
    const payload = chunk.toString('base64');

    ws1.send(
      JSON.stringify({
        type: 'audio.frame',
        callId,
        data: payload,
        seq: i + 1,
        sentAtMs: Date.now(),
      })
    );

    await sleep(frameIntervalMs);
  }
  const sendMs = Math.round(performance.now() - sendStart);

  const sentVideo = new Map(); // seq -> base64 payload
  if (TEST_VIDEO) {
    console.log(`Sending video frames: ${VIDEO_FRAMES} intervalMs=${VIDEO_INTERVAL_MS}`);
    for (let i = 0; i < VIDEO_FRAMES; i++) {
      const payload = Buffer.from(`video_${suffix}_${i + 1}`).toString('base64');
      const seq = i + 1;
      sentVideo.set(seq, payload);
      ws1.send(
        JSON.stringify({
          type: 'video.frame',
          callId,
          data: payload,
          seq,
          sentAtMs: Date.now(),
        })
      );
      await sleep(VIDEO_INTERVAL_MS);
    }
  }

  // wait for delivery
  await sleep(2000);

  ws1.close();
  ws2.close();

  const got = received.size;
  const expected = totalFrames;

  const missing = [];
  for (let seq = 1; seq <= expected; seq++) {
    if (!received.has(seq)) missing.push(seq);
  }

  console.log('='.repeat(72));
  console.log(`Sent frames: ${expected} (send duration ${sendMs}ms)`);
  console.log(`Received frames: ${got}`);
  console.log(`Missing frames: ${missing.length}${missing.length ? ` (first: ${missing.slice(0, 10).join(', ')})` : ''}`);

  const lagStats = summarizeTimings(lagsMs);
  const gapStats = summarizeTimings(gapsMs);
  if (lagStats) console.log('Lag ms stats:', lagStats);
  if (gapStats) console.log('Inter-arrival gap ms stats:', gapStats);

  if (TEST_VIDEO) {
    const missingVideo = [];
    const mismatchedVideo = [];
    for (let seq = 1; seq <= VIDEO_FRAMES; seq++) {
      const recv = receivedVideo.get(seq);
      const sent = sentVideo.get(seq);
      if (!recv) {
        missingVideo.push(seq);
        continue;
      }
      if (sent && recv.data !== sent) {
        mismatchedVideo.push(seq);
      }
    }

    console.log('-'.repeat(72));
    console.log(`Video received frames: ${receivedVideo.size}/${VIDEO_FRAMES}`);
    console.log(
      `Video missing frames: ${missingVideo.length}${
        missingVideo.length ? ` (first: ${missingVideo.slice(0, 10).join(', ')})` : ''
      }`
    );
    console.log(
      `Video mismatched frames: ${mismatchedVideo.length}${
        mismatchedVideo.length ? ` (first: ${mismatchedVideo.slice(0, 10).join(', ')})` : ''
      }`
    );

    if (missingVideo.length || mismatchedVideo.length) {
      process.exit(1);
    }
  }

  if (OUTPUT_DIR) {
    fs.mkdirSync(OUTPUT_DIR, { recursive: true });

    // reconstruct received PCM in seq order
    const parts = [];
    for (let seq = 1; seq <= expected; seq++) {
      const r = received.get(seq);
      if (!r) continue;
      parts.push(Buffer.from(r.data, 'base64'));
    }
    const receivedPcm = Buffer.concat(parts);
    const wavHeader = makeWavHeader({
      numChannels: 1,
      sampleRate: SAMPLE_RATE,
      bitsPerSample: 16,
      dataSize: receivedPcm.length,
    });
    const wav = Buffer.concat([wavHeader, receivedPcm]);
    const outPath = `${OUTPUT_DIR}/received_${suffix}.wav`;
    fs.writeFileSync(outPath, wav);
    console.log(`Wrote ${outPath} (${wav.length} bytes)`);

    const sentWav = Buffer.concat([
      makeWavHeader({ numChannels: 1, sampleRate: SAMPLE_RATE, bitsPerSample: 16, dataSize: pcm.length }),
      pcm,
    ]);
    const sentPath = `${OUTPUT_DIR}/sent_${suffix}.wav`;
    fs.writeFileSync(sentPath, sentWav);
    console.log(`Wrote ${sentPath} (${sentWav.length} bytes)`);
  }

  if (missing.length) {
    process.exit(1);
  }
}

main().catch((err) => {
  console.error(err && err.stack ? err.stack : String(err));
  process.exit(1);
});

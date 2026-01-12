import fs from 'node:fs';
import path from 'node:path';

function usage() {
  console.log(`Usage:
  node backend/scripts/analyze_wav_clicks.mjs <wav-file> [--json]

Env thresholds (optional):
  WINDOW_MS=10                 window size (ms), default 10
  DIFF_THRESHOLD=9000          abs(sample[i]-sample[i-1]) threshold, default 9000
  RATIO_THRESHOLD=10           (maxAbsDiff / (rms+1)) threshold, default 10
`);
}

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

function readWavPcm16le(filePath) {
  const buf = fs.readFileSync(filePath);
  assert(buf.length >= 44, 'file too small for wav');
  assert(buf.toString('ascii', 0, 4) === 'RIFF', 'missing RIFF');
  assert(buf.toString('ascii', 8, 12) === 'WAVE', 'missing WAVE');

  let offset = 12;
  let fmt = null;
  let data = null;
  while (offset + 8 <= buf.length) {
    const id = buf.toString('ascii', offset, offset + 4);
    const size = buf.readUInt32LE(offset + 4);
    const payloadOffset = offset + 8;
    const next = payloadOffset + size + (size % 2);
    if (next > buf.length) break;

    if (id === 'fmt ') {
      const audioFormat = buf.readUInt16LE(payloadOffset + 0);
      const numChannels = buf.readUInt16LE(payloadOffset + 2);
      const sampleRate = buf.readUInt32LE(payloadOffset + 4);
      const bitsPerSample = buf.readUInt16LE(payloadOffset + 14);
      fmt = { audioFormat, numChannels, sampleRate, bitsPerSample };
    } else if (id === 'data') {
      data = { offset: payloadOffset, size };
    }
    offset = next;
  }

  assert(fmt, 'missing fmt chunk');
  assert(data, 'missing data chunk');
  assert(fmt.audioFormat === 1, `unsupported wav format: ${fmt.audioFormat} (want PCM=1)`);
  assert(fmt.bitsPerSample === 16, `unsupported bitsPerSample: ${fmt.bitsPerSample} (want 16)`);
  assert(fmt.numChannels === 1 || fmt.numChannels === 2, `unsupported channels: ${fmt.numChannels}`);

  const dataBuf = buf.subarray(data.offset, data.offset + data.size);
  const sampleCount = Math.floor(dataBuf.length / 2);
  const all = new Int16Array(sampleCount);
  for (let i = 0; i < sampleCount; i++) all[i] = dataBuf.readInt16LE(i * 2);

  if (fmt.numChannels === 1) return { fmt, samples: all };

  const mono = new Int16Array(Math.floor(sampleCount / 2));
  for (let i = 0; i < mono.length; i++) {
    const l = all[i * 2];
    const r = all[i * 2 + 1];
    mono[i] = Math.round((l + r) / 2);
  }
  return { fmt: { ...fmt, numChannels: 1 }, samples: mono };
}

function summarize(values) {
  const v = values.filter((x) => Number.isFinite(x)).sort((a, b) => a - b);
  if (!v.length) return null;
  const sum = v.reduce((a, b) => a + b, 0);
  const p = (q) => v[Math.floor((v.length - 1) * q)];
  return { count: v.length, min: v[0], p50: p(0.5), p90: p(0.9), p99: p(0.99), max: v[v.length - 1], avg: sum / v.length };
}

function main() {
  const args = process.argv.slice(2);
  if (!args.length || args.includes('--help') || args.includes('-h')) {
    usage();
    process.exit(args.length ? 0 : 1);
  }

  const filePath = args[0];
  const asJson = args.includes('--json');

  const windowMs = Number(process.env.WINDOW_MS || 10);
  const diffThreshold = Number(process.env.DIFF_THRESHOLD || 9000);
  const ratioThreshold = Number(process.env.RATIO_THRESHOLD || 10);

  const { fmt, samples } = readWavPcm16le(filePath);
  const sr = fmt.sampleRate;
  const windowSamples = Math.max(1, Math.round((sr * windowMs) / 1000));

  const candidates = [];
  let last = samples[0] || 0;

  for (let w = 0; w < Math.floor(samples.length / windowSamples); w++) {
    const start = w * windowSamples;
    const end = Math.min(samples.length, start + windowSamples);
    let maxAbsDiff = 0;
    let sumSq = 0;

    for (let i = start; i < end; i++) {
      const s = samples[i];
      const d = Math.abs(s - last);
      if (d > maxAbsDiff) maxAbsDiff = d;
      last = s;
      sumSq += s * s;
    }

    const rms = Math.sqrt(sumSq / Math.max(1, end - start));
    const ratio = maxAbsDiff / (rms + 1);

    if (maxAbsDiff >= diffThreshold && ratio >= ratioThreshold) {
      const tMs = (start / sr) * 1000;
      candidates.push({ tMs, maxAbsDiff, rms, ratio });
    }
  }

  const intervalsMs = [];
  for (let i = 1; i < candidates.length; i++) intervalsMs.push(candidates[i].tMs - candidates[i - 1].tMs);

  const result = {
    file: path.resolve(filePath),
    sampleRate: sr,
    durationSec: samples.length / sr,
    thresholds: { windowMs, diffThreshold, ratioThreshold },
    candidates: {
      count: candidates.length,
      first10: candidates.slice(0, 10),
    },
    intervalsMs: summarize(intervalsMs),
  };

  if (asJson) {
    console.log(JSON.stringify(result, null, 2));
    return;
  }

  console.log(`file=${result.file}`);
  console.log(`sampleRate=${result.sampleRate}Hz duration=${result.durationSec.toFixed(2)}s`);
  console.log(`thresholds: windowMs=${windowMs} diff>=${diffThreshold} ratio>=${ratioThreshold}`);
  console.log(`candidates=${result.candidates.count}`);
  if (result.candidates.first10.length) {
    console.log('first10 candidates (tMs, maxAbsDiff, rms, ratio):');
    for (const c of result.candidates.first10) {
      console.log(`  ${Math.round(c.tMs)}ms diff=${c.maxAbsDiff} rms=${Math.round(c.rms)} ratio=${c.ratio.toFixed(2)}`);
    }
  }
  console.log(`intervalsMs: ${result.intervalsMs ? JSON.stringify(result.intervalsMs) : 'n/a'}`);
}

try {
  main();
} catch (err) {
  console.error(err && err.stack ? err.stack : String(err));
  process.exit(1);
}


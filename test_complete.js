const WebSocket = require('ws');
const http = require('http');

const BASE_URL = 'http://103.40.13.96:8081';
const WS_URL = 'ws://103.40.13.96:8081';

const results = { passed: 0, failed: 0, tests: [] };

function log(message) {
  console.log(`[${new Date().toISOString()}] ${message}`);
}

function assert(condition, testName) {
  if (condition) {
    results.passed++;
    results.tests.push({ name: testName, status: 'PASS' });
    log(`✓ ${testName}`);
  } else {
    results.failed++;
    results.tests.push({ name: testName, status: 'FAIL' });
    log(`✗ ${testName}`);
  }
}

async function httpRequest(method, path, data, token) {
  return new Promise((resolve, reject) => {
    const postData = data ? JSON.stringify(data) : null;
    const options = {
      hostname: '103.40.13.96',
      port: 8081,
      path,
      method,
      headers: {
        'Content-Type': 'application/json',
        ...(token && { 'Authorization': `Bearer ${token}` }),
        ...(postData && { 'Content-Length': Buffer.byteLength(postData) })
      }
    };

    const req = http.request(options, (res) => {
      let responseData = '';
      res.on('data', chunk => responseData += chunk);
      res.on('end', () => {
        if (res.statusCode >= 200 && res.statusCode < 300) {
          try {
            resolve(responseData ? JSON.parse(responseData) : {});
          } catch (e) {
            // Response is not JSON (like "ok" from healthz)
            resolve({ raw: responseData });
          }
        } else {
          reject(new Error(`HTTP ${res.statusCode}: ${responseData}`));
        }
      });
    });

    req.on('error', reject);
    if (postData) req.write(postData);
    req.end();
  });
}

async function testHealthCheck() {
  try {
    await httpRequest('GET', '/healthz');
    assert(true, 'Health check endpoint');
  } catch (e) {
    assert(false, 'Health check endpoint');
  }
}

async function testReadyCheck() {
  try {
    await httpRequest('GET', '/readyz');
    assert(true, 'Ready check endpoint');
  } catch (e) {
    assert(false, 'Ready check endpoint');
  }
}

async function testCompleteCallFlow() {
  const timestamp = Date.now().toString().slice(-8);
  const password = 'TestP@ss123!';

  try {
    // Register two users
    const user1 = await httpRequest('POST', '/v1/auth/register', {
      username: `caller_${timestamp}`,
      password,
      displayName: `Caller ${timestamp}`
    });
    log(`Caller created: ${user1.user.id}`);

    const user2 = await httpRequest('POST', '/v1/auth/register', {
      username: `callee_${timestamp}`,
      password,
      displayName: `Callee ${timestamp}`
    });
    log(`Callee created: ${user2.user.id}`);

    // Create session between users
    const session = await httpRequest('POST', '/v1/sessions', {
      peerUserId: user2.user.id
    }, user1.token);
    log(`Session created: ${session.session.id}`);

    // Create a call
    const call = await httpRequest('POST', '/v1/calls', {
      calleeUserId: user2.user.id,
      mediaType: 'voice'
    }, user1.token);
    log(`Call created: ${call.call.id}`);

    // Accept the call
    await httpRequest('POST', `/v1/calls/${call.call.id}/accept`, {}, user2.token);
    log(`Call accepted`);

    // Now test frame relay with valid call
    await testFrameRelayWithCall(user1.token, user2.token, call.call.id);

    assert(true, 'Complete call flow');
  } catch (e) {
    log(`Call flow error: ${e.message}`);
    assert(false, 'Complete call flow');
  }
}

async function testFrameRelayWithCall(token1, token2, callId) {
  return new Promise((resolve) => {
    const ws1 = new WebSocket(`${WS_URL}/v1/ws?token=${token1}`);
    const ws2 = new WebSocket(`${WS_URL}/v1/ws?token=${token2}`);

    let ws1Ready = false;
    let ws2Ready = false;
    let audioReceived = false;
    let videoReceived = false;

    ws1.on('open', () => {
      ws1Ready = true;
      checkReady();
    });

    ws2.on('open', () => {
      ws2Ready = true;
      checkReady();
    });

    function checkReady() {
      if (ws1Ready && ws2Ready) {
        // Send audio frame
        ws1.send(JSON.stringify({
          type: 'audio.frame',
          callId,
          data: Buffer.from('test audio').toString('base64')
        }));

        // Send video frame
        setTimeout(() => {
          ws1.send(JSON.stringify({
            type: 'video.frame',
            callId,
            data: Buffer.from('test video').toString('base64')
          }));
        }, 100);
      }
    }

    ws2.on('message', (data) => {
      try {
        const msg = JSON.parse(data.toString());
        if (msg.type === 'audio.frame' && msg.payload?.callId === callId) {
          audioReceived = true;
          log('Audio frame received');
        }
        if (msg.type === 'video.frame' && msg.payload?.callId === callId) {
          videoReceived = true;
          log('Video frame received');
        }
      } catch (e) {}
    });

    setTimeout(() => {
      assert(audioReceived, 'Audio frame relay with valid call');
      assert(videoReceived, 'Video frame relay with valid call');
      ws1.close();
      ws2.close();
      resolve();
    }, 2000);

    ws1.on('error', () => {});
    ws2.on('error', () => {});
  });
}

async function testHighFrequencyWithCall() {
  const timestamp = Date.now().toString().slice(-8);
  const password = 'TestP@ss123!';

  try {
    // Create users and call
    const user1 = await httpRequest('POST', '/v1/auth/register', {
      username: `hf1_${timestamp}`,
      password,
      displayName: `HF1`
    });

    const user2 = await httpRequest('POST', '/v1/auth/register', {
      username: `hf2_${timestamp}`,
      password,
      displayName: `HF2`
    });

    await httpRequest('POST', '/v1/sessions', {
      peerUserId: user2.user.id
    }, user1.token);

    const call = await httpRequest('POST', '/v1/calls', {
      calleeUserId: user2.user.id,
      mediaType: 'voice'
    }, user1.token);

    await httpRequest('POST', `/v1/calls/${call.call.id}/accept`, {}, user2.token);

    // Test high frequency
    await new Promise((resolve) => {
      const ws1 = new WebSocket(`${WS_URL}/v1/ws?token=${user1.token}`);
      const ws2 = new WebSocket(`${WS_URL}/v1/ws?token=${user2.token}`);

      let sentCount = 0;
      let receivedCount = 0;
      const totalFrames = 100;

      ws1.on('open', () => {
        ws2.on('open', () => {
          const interval = setInterval(() => {
            if (sentCount >= totalFrames) {
              clearInterval(interval);
              return;
            }
            ws1.send(JSON.stringify({
              type: 'audio.frame',
              callId: call.call.id,
              data: Buffer.from(`frame${sentCount}`).toString('base64')
            }));
            sentCount++;
          }, 10);
        });
      });

      ws2.on('message', (data) => {
        try {
          const msg = JSON.parse(data.toString());
          if (msg.type === 'audio.frame') receivedCount++;
        } catch (e) {}
      });

      setTimeout(() => {
        const rate = (receivedCount / totalFrames) * 100;
        assert(rate >= 90, `High frequency (${receivedCount}/${totalFrames} = ${rate.toFixed(1)}%)`);
        ws1.close();
        ws2.close();
        resolve();
      }, 3000);
    });
  } catch (e) {
    log(`High frequency test error: ${e.message}`);
    assert(false, 'High frequency frames');
  }
}

async function runTests() {
  log('Starting comprehensive external tests...\n');

  await testHealthCheck();
  await testReadyCheck();
  await testCompleteCallFlow();
  await testHighFrequencyWithCall();

  log('\n' + '='.repeat(50));
  log('TEST RESULTS');
  log('='.repeat(50));
  results.tests.forEach(test => {
    log(`${test.status === 'PASS' ? '✓' : '✗'} ${test.name}`);
  });
  log('='.repeat(50));
  log(`Total: ${results.passed + results.failed} tests`);
  log(`Passed: ${results.passed}`);
  log(`Failed: ${results.failed}`);
  log('='.repeat(50));

  process.exit(results.failed > 0 ? 1 : 0);
}

runTests().catch(err => {
  log(`Fatal error: ${err.message}`);
  process.exit(1);
});

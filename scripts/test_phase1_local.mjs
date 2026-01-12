const BASE_URL = process.env.BASE_URL || 'http://127.0.0.1:18083';

function sleep(ms) {
  return new Promise((r) => setTimeout(r, ms));
}

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

function suffix() {
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
  let json;
  try {
    json = text ? JSON.parse(text) : {};
  } catch {
    json = { raw: text };
  }
  return { ok: res.ok, status: res.status, json, text };
}

async function mustOk(res, label) {
  if (res.ok) return res;
  throw new Error(`${label} failed: HTTP ${res.status} ${res.text}`);
}

async function registerUser(username, displayName) {
  const password = 'TestP@ss123!';
  const res = await http('POST', '/v1/auth/register', { username, password, displayName }, '');
  await mustOk(res, `register ${username}`);
  const userId = res.json?.user?.id;
  const token = res.json?.token;
  assert(userId && token, `register ${username}: missing id/token`);
  return { userId, token };
}

async function main() {
  const s = suffix();
  console.log(`BASE_URL=${BASE_URL}`);

  // --- Home Base + Local Feed
  const alice = await registerUser(`alice_${s}`, `Alice_${s}`);
  const bob = await registerUser(`bob_${s}`, `Bob_${s}`);

  // Set home base.
  await mustOk(
    await http('PUT', '/v1/home-base', { lat: 31.0, lng: 121.0 }, alice.token),
    'PUT /v1/home-base'
  );

  // Same-day update should be limited.
  const hb2 = await http('PUT', '/v1/home-base', { lat: 31.1, lng: 121.1 }, alice.token);
  assert(hb2.status === 429, `expected HOME_BASE_UPDATE_LIMITED status 429, got ${hb2.status}`);
  assert(
    hb2.json?.error?.code === 'HOME_BASE_UPDATE_LIMITED',
    `expected error.code HOME_BASE_UPDATE_LIMITED, got ${hb2.json?.error?.code}`
  );

  // Create a local feed post (defaults: radius=1km, expires=30d, not pinned).
  const createPost = await mustOk(
    await http('POST', '/v1/local-feed/posts', { text: 'hello' }, alice.token),
    'POST /v1/local-feed/posts'
  );
  const postId = createPost.json?.post?.id;
  assert(postId, 'expected post.id');
  assert(createPost.json?.post?.radiusM === 1000, `expected radiusM=1000, got ${createPost.json?.post?.radiusM}`);
  assert(createPost.json?.post?.isPinned === false, `expected isPinned=false, got ${createPost.json?.post?.isPinned}`);

  // Pins should include Alice.
  const pins = await mustOk(
    await http(
      'GET',
      '/v1/local-feed/pins?minLat=30&maxLat=32&minLng=120&maxLng=122&centerLat=31&centerLng=121&limit=50',
      null,
      alice.token
    ),
    'GET /v1/local-feed/pins'
  );
  const pinsArr = pins.json?.pins || [];
  assert(Array.isArray(pinsArr), 'pins must be array');
  assert(pinsArr.some((p) => p.userId === alice.userId), 'expected pins to include alice');

  // Bob far away should not see Alice's post; near should see it.
  const far = await mustOk(
    await http('GET', `/v1/local-feed/users/${encodeURIComponent(alice.userId)}/posts?atLat=0&atLng=0`, null, bob.token),
    'GET /v1/local-feed/users/{id}/posts far'
  );
  assert((far.json?.posts || []).length === 0, `expected 0 posts when far, got ${(far.json?.posts || []).length}`);

  const near = await mustOk(
    await http(
      'GET',
      `/v1/local-feed/users/${encodeURIComponent(alice.userId)}/posts?atLat=31&atLng=121`,
      null,
      bob.token
    ),
    'GET /v1/local-feed/users/{id}/posts near'
  );
  assert((near.json?.posts || []).length >= 1, `expected >=1 posts when near, got ${(near.json?.posts || []).length}`);

  // --- Profiles (core + card/map overrides)
  await mustOk(await http('GET', '/v1/profiles/card', null, alice.token), 'GET /v1/profiles/card');
  await mustOk(await http('PUT', '/v1/users/me', { avatarUrl: '/uploads/core.png' }, alice.token), 'PUT /v1/users/me avatar');
  const card2 = await mustOk(await http('GET', '/v1/profiles/card', null, alice.token), 'GET /v1/profiles/card (2)');
  assert(card2.json?.profile?.avatarUrl === '/uploads/core.png', 'expected card profile resolved avatarUrl from core');

  // --- Relationship groups + session relationship meta
  const group = await mustOk(
    await http('POST', '/v1/relationship-groups', { name: 'Group1' }, alice.token),
    'POST /v1/relationship-groups'
  );
  const groupId = group.json?.group?.id;
  assert(groupId, 'expected group.id');

  const sessionRes = await mustOk(
    await http('POST', '/v1/sessions', { peerUserId: bob.userId }, alice.token),
    'POST /v1/sessions'
  );
  const sessionId = sessionRes.json?.session?.id;
  assert(sessionId, 'expected session.id');

  await mustOk(
    await http(
      'PUT',
      `/v1/sessions/${encodeURIComponent(sessionId)}/relationship`,
      { note: 'hello', groupId, tags: ['t2', 't1', 't1'] },
      alice.token
    ),
    'PUT /v1/sessions/{id}/relationship'
  );
  const rel = await mustOk(
    await http('GET', `/v1/sessions/${encodeURIComponent(sessionId)}/relationship`, null, alice.token),
    'GET /v1/sessions/{id}/relationship'
  );
  assert(rel.json?.relationship?.groupName === 'Group1', `expected groupName=Group1, got ${rel.json?.relationship?.groupName}`);

  await mustOk(
    await http('POST', `/v1/relationship-groups/${encodeURIComponent(groupId)}/rename`, { name: 'G2' }, alice.token),
    'POST /v1/relationship-groups/{id}/rename'
  );
  const rel2 = await mustOk(
    await http('GET', `/v1/sessions/${encodeURIComponent(sessionId)}/relationship`, null, alice.token),
    'GET /v1/sessions/{id}/relationship (2)'
  );
  assert(rel2.json?.relationship?.groupName === 'G2', 'expected groupName updated to G2');

  await mustOk(
    await http('POST', `/v1/relationship-groups/${encodeURIComponent(groupId)}/delete`, {}, alice.token),
    'POST /v1/relationship-groups/{id}/delete'
  );
  const rel3 = await mustOk(
    await http('GET', `/v1/sessions/${encodeURIComponent(sessionId)}/relationship`, null, alice.token),
    'GET /v1/sessions/{id}/relationship (3)'
  );
  assert(rel3.json?.relationship?.groupId == null, 'expected groupId cleared after delete');

  // --- Session requests (map) + default group assignment
  const reqUser = await registerUser(`req_${s}`, `Req_${s}`);
  const addUser = await registerUser(`add_${s}`, `Add_${s}`);

  const createdReq = await mustOk(
    await http(
      'POST',
      '/v1/session-requests',
      { addresseeId: addUser.userId, verificationMessage: 'hi' },
      reqUser.token
    ),
    'POST /v1/session-requests'
  );
  const reqId = createdReq.json?.request?.id;
  assert(reqId, 'expected session request id');

  const accept = await mustOk(
    await http('POST', `/v1/session-requests/${encodeURIComponent(reqId)}/accept`, {}, addUser.token),
    'POST /v1/session-requests/{id}/accept'
  );
  const mapSessionId = accept.json?.session?.id;
  assert(mapSessionId, 'expected session id from accept');

  const relA = await mustOk(
    await http('GET', `/v1/sessions/${encodeURIComponent(mapSessionId)}/relationship`, null, reqUser.token),
    'GET map session relationship (requester)'
  );
  const relB = await mustOk(
    await http('GET', `/v1/sessions/${encodeURIComponent(mapSessionId)}/relationship`, null, addUser.token),
    'GET map session relationship (addressee)'
  );
  assert(relA.json?.relationship?.groupName === '地图', `expected requester groupName=地图, got ${relA.json?.relationship?.groupName}`);
  assert(relB.json?.relationship?.groupName === '地图', `expected addressee groupName=地图, got ${relB.json?.relationship?.groupName}`);

  // --- Rate limit (10 per day) for map requests
  const lim = await registerUser(`lim_${s}`, `Lim_${s}`);
  for (let i = 1; i <= 11; i++) {
    const u = await registerUser(`lim_u${i}_${s}`, `U${i}`);
    const res = await http('POST', '/v1/session-requests', { addresseeId: u.userId }, lim.token);
    if (i <= 10) {
      await mustOk(res, `rate limit request ${i}`);
    } else {
      assert(res.status === 429, `expected 11th request 429, got ${res.status}`);
      assert(res.json?.error?.code === 'RATE_LIMITED', `expected RATE_LIMITED, got ${res.json?.error?.code}`);
    }
    await sleep(10);
  }

  // --- Cooldown after reject (3 days) basic check: immediate re-send should be blocked
  const ca = await registerUser(`cool_a_${s}`, `CoolA_${s}`);
  const cb = await registerUser(`cool_b_${s}`, `CoolB_${s}`);
  const first = await mustOk(
    await http('POST', '/v1/session-requests', { addresseeId: cb.userId }, ca.token),
    'cooldown create request'
  );
  const firstId = first.json?.request?.id;
  assert(firstId, 'expected cooldown request id');
  await mustOk(
    await http('POST', `/v1/session-requests/${encodeURIComponent(firstId)}/reject`, {}, cb.token),
    'cooldown reject'
  );
  const second = await http('POST', '/v1/session-requests', { addresseeId: cb.userId }, ca.token);
  assert(second.status === 429, `expected cooldown resend 429, got ${second.status}`);
  assert(second.json?.error?.code === 'COOLDOWN_ACTIVE', `expected COOLDOWN_ACTIVE, got ${second.json?.error?.code}`);

  console.log('Phase1 external tests: PASS');
}

main().catch((err) => {
  console.error(err && err.stack ? err.stack : String(err));
  process.exit(1);
});


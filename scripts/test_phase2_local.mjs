const BASE_URL = process.env.BASE_URL || 'http://127.0.0.1:18084';

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

  const creator = await registerUser(`creator_${s}`, `Creator_${s}`);
  const member = await registerUser(`member_${s}`, `Member_${s}`);

  const endAtMs = Date.now() + 60 * 60 * 1000;
  const created = await mustOk(
    await http('POST', '/v1/activities', { title: 'Test Activity', endAtMs }, creator.token),
    'POST /v1/activities'
  );
  const activityId = created.json?.activity?.id;
  const sessionId = created.json?.activity?.sessionId;
  const inviteCode = created.json?.inviteCode;
  assert(activityId && sessionId && inviteCode, 'expected activity.id/activity.sessionId/inviteCode');

  const joined = await mustOk(
    await http('POST', '/v1/activities/invites/consume', { code: inviteCode }, member.token),
    'POST /v1/activities/invites/consume'
  );
  assert(joined.json?.joined === true, 'expected joined=true');

  const members = await mustOk(
    await http('GET', `/v1/activities/${encodeURIComponent(activityId)}/members`, null, member.token),
    'GET /v1/activities/{id}/members'
  );
  const list = members.json?.members || [];
  assert(Array.isArray(list) && list.length >= 2, `expected >=2 members, got ${list.length}`);

  await mustOk(
    await http(
      'POST',
      `/v1/activities/${encodeURIComponent(activityId)}/members/${encodeURIComponent(member.userId)}/remove`,
      {},
      creator.token
    ),
    'POST remove member'
  );

  const msg = await http(
    'POST',
    `/v1/sessions/${encodeURIComponent(sessionId)}/messages`,
    { type: 'text', text: 'hello' },
    member.token
  );
  assert(msg.status === 403, `expected removed member cannot send message (403), got ${msg.status}`);
  assert(msg.json?.error?.code === 'SESSION_ACCESS_DENIED', `expected SESSION_ACCESS_DENIED, got ${msg.json?.error?.code}`);

  console.log('Phase2 external tests: PASS');
}

main().catch((err) => {
  console.error(err && err.stack ? err.stack : String(err));
  process.exit(1);
});


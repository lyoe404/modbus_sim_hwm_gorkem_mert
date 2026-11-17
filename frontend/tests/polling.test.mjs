import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import ts from 'typescript';

async function load(file) {
  const { outputText } = ts.transpileModule(readFileSync(new URL(file, import.meta.url), 'utf8'), {
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
  });
  return import(`data:text/javascript;base64,${Buffer.from(outputText).toString('base64')}`);
}
const { createPolling } = await load('../src/polling.ts');
const { api, ApiError } = await load('../src/api.ts');
const deferred = () => { let resolve; const promise = new Promise(r => { resolve = r; }); return { promise, resolve }; };
const flush = () => new Promise(resolve => setImmediate(resolve));

function timers() {
  let tick;
  let cleared = false;
  globalThis.window = { setInterval(callback) { tick = callback; return 1; }, clearInterval() { cleared = true; } };
  return { tick: () => tick(), get cleared() { return cleared; } };
}

test('slow cycle shares manual and scheduled refreshes until every task settles', async () => {
  const clock = timers();
  const slow = deferred();
  let calls = 0;
  const poll = createPolling(async () => {
    calls++;
    await Promise.allSettled([Promise.reject(new Error('device offline')), slow.promise]);
  }, 2000);
  const first = poll.run();
  await flush();
  clock.tick();
  assert.equal(poll.run(), first);
  assert.equal(calls, 1);
  slow.resolve();
  await first;
  await poll.run();
  assert.equal(calls, 2);
  poll.stop();
});

test('cleanup aborts the old session and prevents further polling', async () => {
  const clock = timers();
  const slow = deferred();
  let signal;
  let updates = 0;
  const poll = createPolling(async current => {
    signal = current;
    await slow.promise;
    if (!current.aborted) updates++;
  }, 2000);
  const pending = poll.run();
  await flush();
  poll.stop();
  assert.equal(signal.aborted, true);
  assert.equal(clock.cleared, true);
  slow.resolve();
  await pending;
  clock.tick();
  await poll.run();
  assert.equal(updates, 0);
});

test('a rejected cycle releases the lock for recovery', async () => {
  timers();
  let calls = 0;
  const poll = createPolling(async () => { if (++calls === 1) throw new Error('offline'); }, 2000);
  await assert.rejects(poll.run(), /offline/);
  await poll.run();
  assert.equal(calls, 2);
  poll.stop();
});

test('device failure does not prevent an independent unauthenticated health check', async () => {
  const requests = [];
  globalThis.fetch = async (path, init) => {
    requests.push({ path, init });
    return path === '/healthz'
      ? new Response(JSON.stringify({ status: 'ok' }), { status: 200 })
      : new Response(JSON.stringify({ error: 'device_unreachable' }), { status: 502 });
  };
  await assert.rejects(api.relays('token'), error => error instanceof ApiError && error.code === 'device_unreachable');
  assert.deepEqual(await api.health(), { status: 'ok' });
  assert.equal(requests[1].init.headers.Authorization, undefined);
  assert.equal(requests[1].init.cache, 'no-store');
});

test('health reports backend outage and recovers on the next request', async () => {
  globalThis.fetch = async () => { throw new TypeError('network down'); };
  await assert.rejects(api.health(), error => error.code === 'backend_unreachable');
  globalThis.fetch = async () => new Response(JSON.stringify({ status: 'ok' }));
  assert.deepEqual(await api.health(), { status: 'ok' });
});

test('polling endpoints forward cancellation to fetch', async () => {
  const controller = new AbortController();
  const signals = [];
  globalThis.fetch = async (_path, init) => { signals.push(init.signal); return new Response('[]'); };
  await Promise.all([api.devices('t', controller.signal), api.measurements('t', 'sensor', 'temperature_c', 50, controller.signal),
    api.relays('t', controller.signal), api.events('t', 30, controller.signal), api.getControl('t', controller.signal)]);
  controller.abort();
  assert.equal(signals.length, 5);
  assert.ok(signals.every(signal => signal.aborted));
});

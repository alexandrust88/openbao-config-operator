import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { guardOutbound, localProxy, validateConfig } from './browser.mjs';

const config = { provider: 'okta', issuer: 'https://example.okta.test/oauth2/lab',
  url: 'https://example.okta.test/oauth2/lab/v1/authorize?state=fake-state',
  state: 'fake-state', username: 'fake-user', password: 'fake-password' };

test('hosted authorization stays on issuer origin and binds state', () => {
  assert.equal(validateConfig(config).origin, 'https://example.okta.test');
  for (const changes of [{ url: 'https://untrusted.test/authorize?state=fake-state' },
    { state: 'different-state' }, { url: 'http://example.okta.test/authorize?state=fake-state' },
    { password: '' }, { provider: 'unknown' }]) {
    assert.throws(() => validateConfig({ ...config, ...changes }), /Invalid browser input/);
  }
});


test('cross-origin credential-bearing requests are aborted, never sent', async () => {
  for (const navigation of [false, true]) {
    let aborted = 0;
    let sent = 0;
    const route = {
      request: () => ({ url: () => 'https://untrusted.test/collect', method: () => 'POST',
        isNavigationRequest: () => navigation, frame: () => ({ parentFrame: () => null }), postData: () => 'password=fixture-secret' }),
      abort: async () => { aborted += 1; },
      continue: async () => { sent += 1; },
    };
    assert.equal(await guardOutbound(route, 'https://example.okta.test'), navigation);
    assert.equal(aborted, 1);
    assert.equal(sent, 0);
  }
});

test('same-origin form posts remain allowed', async () => {
  let sent = false;
  const route = {
    request: () => ({ url: () => 'https://example.okta.test/login', method: () => 'POST', isNavigationRequest: () => true }),
    abort: async () => { assert.fail('same-origin form must not be aborted'); },
    continue: async () => { sent = true; },
  };
  assert.equal(await guardOutbound(route, 'https://example.okta.test'), false);
  assert.equal(sent, true);
});


test('foreign background frames remain blocked without failing the main login', async () => {
  let aborted = false;
  const route = {
    request: () => ({ url: () => 'https://untrusted.test/frame', method: () => 'GET',
      isNavigationRequest: () => true, frame: () => ({ parentFrame: () => ({}) }) }),
    abort: async () => { aborted = true; },
    continue: async () => { assert.fail('foreign frame must remain blocked'); },
  };
  assert.equal(await guardOutbound(route, 'https://example.okta.test'), false);
  assert.equal(aborted, true);
});


test('HTTP routing is restricted to the exact disposable Keycloak issuer and loopback relay', () => {
  const local = { ...config, provider: 'keycloak', issuer: 'http://keycloak.obco-lab-identity.svc:8080/realms/obco-lab',
    url: 'http://keycloak.obco-lab-identity.svc:8080/realms/obco-lab/protocol/openid-connect/auth?state=fake-state',
    route_host: '127.0.0.1', route_port: 12345 };
  assert.equal(validateConfig(local).protocol, 'http:');
  for (const changes of [{ provider: 'auth0' }, { provider: 'okta' }, { route_host: 'untrusted.test' },
    { route_port: 0 }, { issuer: 'https://untrusted.test', url: 'https://untrusted.test/auth?state=fake-state' }]) {
    assert.throws(() => validateConfig({ ...local, ...changes }), /Invalid browser input/);
  }
});

for (const [provider, issuer] of [
  ['authentik', 'http://authentik.obco-lab-authentik.svc:9000/application/o/openbao/'],
  ['zitadel', 'http://zitadel.obco-lab-zitadel.svc:8080'],
]) {
  test(`${provider} relay accepts only its exact issuer and trusted loopback destination`, () => {
    const local = { ...config, provider, issuer, url: issuer + '/authorize?state=fake-state', route_host: '127.0.0.1', route_port: 12345 };
    assert.equal(validateConfig(local).protocol, 'http:');
    for (const changes of [{ issuer: issuer + '/other' }, { provider: 'auth0' }, { provider: 'okta' },
      { route_host: 'untrusted.test' }, { route_port: 0 }, { url: 'http://untrusted.test/authorize?state=fake-state' }]) {
      assert.throws(() => validateConfig({ ...local, ...changes }), /Invalid browser input/);
    }
  });
}

test('local proxy refuses foreign credential requests before contacting its upstream', async () => {
  let forwarded = 0;
  const upstream = http.createServer((request, response) => {
    forwarded += 1;
    assert.equal(request.headers.host, 'zitadel.obco-lab-zitadel.svc:8080');
    if (request.url === '/redirect') {
      response.writeHead(307, { location: 'http://127.0.0.1:9999/collect' }); response.end(); return;
    }
    response.writeHead(204); response.end();
  });
  await new Promise(resolve => upstream.listen(0, '127.0.0.1', resolve));
  const proxy = localProxy({ route_host: '127.0.0.1', route_port: upstream.address().port }, 'http://zitadel.obco-lab-zitadel.svc:8080');
  await new Promise(resolve => proxy.listen(0, '127.0.0.1', resolve));
  const send = destination => new Promise((resolve, reject) => {
    const request = http.request({ hostname: '127.0.0.1', port: proxy.address().port, method: 'POST', path: destination }, response => {
      response.resume(); response.on('end', () => resolve(response.statusCode));
    });
    request.on('error', reject); request.end('fixture-credential');
  });
  try {
    assert.equal(await send('http://foreign.test/collect'), 403);
    assert.equal(forwarded, 0);
    assert.equal(await send('http://zitadel.obco-lab-zitadel.svc:8080/token'), 204);
    assert.equal(forwarded, 1);
    assert.equal(await send('http://zitadel.obco-lab-zitadel.svc:8080/redirect'), 403);
    assert.equal(forwarded, 2); // Its redirect is refused, never forwarded to another origin.
  } finally {
    for (const server of [proxy, upstream]) {
      server.closeAllConnections();
      await new Promise(resolve => server.close(resolve));
    }
  }
});

test('closing a browser request after its body completes closes the hanging upstream', { timeout: 5000 }, async () => {
  let client;
  let closed;
  const upstreamClosed = new Promise(resolve => { closed = resolve; });
  const upstream = http.createServer((request, response) => {
    response.on('close', closed);
    request.resume();
    request.on('end', () => client.destroy());
  });
  await new Promise(resolve => upstream.listen(0, '127.0.0.1', resolve));
  const proxy = localProxy({ route_host: '127.0.0.1', route_port: upstream.address().port }, 'http://zitadel.obco-lab-zitadel.svc:8080');
  await new Promise(resolve => proxy.listen(0, '127.0.0.1', resolve));
  try {
    client = http.request({ hostname: '127.0.0.1', port: proxy.address().port, method: 'POST',
      path: 'http://zitadel.obco-lab-zitadel.svc:8080/token' });
    client.on('error', () => {});
    client.end('fixture-body');
    await upstreamClosed;
  } finally {
    client?.destroy();
    for (const server of [proxy, upstream]) {
      server.closeAllConnections();
      await new Promise(resolve => server.close(resolve));
    }
  }
});

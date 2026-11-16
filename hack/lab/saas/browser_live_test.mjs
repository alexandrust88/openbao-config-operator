// Real Chromium regression against isolated synthetic hosted forms; no SaaS account needed.
import test from 'node:test';
import http from 'node:http';
import { randomBytes } from 'node:crypto';
import assert from 'node:assert/strict';
import { authenticate } from './browser.mjs';

const origin = 'https://idp.example.test';
const config = { provider: 'auth0', issuer: origin + '/', url: origin + '/authorize?state=fake-state',
  state: 'fake-state', username: 'fixture-user', password: randomBytes(32).toString('base64url') };

function fixture({ provider = 'auth0', twoStep = false, badState = false, challenge = false, telemetry = false, externalForm = false, backgroundFrame = false, asyncForm = false } = {}) {
  let submitted = false;
  let submissions = 0;
  const setup = async context => {
    await context.route(origin + '/**', async route => {
      const req = route.request();
      const path = new URL(req.url()).pathname;
      const fields = new URLSearchParams(req.postData() || '');
      if (path === '/authorize') {
        const input = provider === 'okta' ? 'identifier' : 'username';
        const passwordField = twoStep ? '' : '<input type="password" name="password">';
        const analytics = telemetry ? '<script>fetch("https://metrics.example.test/collect", {method:"POST", body:"fixture-telemetry"}).catch(() => {});</script>' : '';
        const frame = backgroundFrame ? '<iframe src="https://untrusted.test/frame" hidden></iframe>' : '';
        const handler = asyncForm ? `<script>document.addEventListener('submit', async event => {event.preventDefault();const response=await fetch('/login',{method:'POST',body:new URLSearchParams(new FormData(event.target))});const result=await response.json();location.assign(result.url);});</script>` : '';
        await route.fulfill({ contentType: 'text/html', body:
          `${analytics}${frame}${handler}<form action="${externalForm ? 'https://untrusted.test/collect' : (twoStep ? '/password' : '/login')}" method="post"><input name="${input}" autocomplete="username">${passwordField}<button type="submit">Continue</button></form>` });
      } else if (path === '/password') {
        assert.equal(fields.get(provider === 'okta' ? 'identifier' : 'username'), 'fixture-user');
        const input = provider === 'okta' ? 'credentials.passcode' : 'password';
        await route.fulfill({ contentType: 'text/html', body:
          `<form action="/login" method="post"><input name="${input}" type="password"><input type="submit" value="Verify"></form>` });
      } else if (path === '/login') {
        assert.equal(fields.get(provider === 'okta' ? 'credentials.passcode' : 'password'), config.password);
        submitted = true;
        submissions += 1;
        if (asyncForm) {
          await new Promise(resolve => setTimeout(resolve, 3500));
          await route.fulfill({ contentType: 'application/json', body: JSON.stringify({url: 'http://127.0.0.1:8250/oidc/callback?code=fixture-code&state=fake-state'}) });
        } else if (challenge) {
          await route.fulfill({ contentType: 'text/html', body: '<h1>Enter verification code</h1><input autocomplete="one-time-code">' });
        } else {
          await route.fulfill({ status: 302, headers: { location:
            'http://127.0.0.1:8250/oidc/callback?code=fixture-code&state=' + (badState ? 'wrong-state' : 'fake-state') } });
        }
      } else {
        await route.abort();
      }
    });
  };
  return { setup, submitted: () => submitted, submissions: () => submissions };
}

for (const [name, options] of [
  ['Auth0 combined username/password', {}],
  ['Blocked telemetry does not fail a valid hosted login', { telemetry: true }],
  ['Blocked background frame does not fail a valid hosted login', { backgroundFrame: true }],
  ['Slow asynchronous login is submitted only once', { asyncForm: true }],
  ['Auth0 identifier-first', { twoStep: true }],
  ['Okta Identity Engine identifier/password', { provider: 'okta', twoStep: true }],
]) {
  test(name, { timeout: 45000 }, async () => {
    const mock = fixture(options);
    const result = await authenticate({ ...config, provider: options.provider || 'auth0' }, mock.setup);
    assert.deepEqual(result, { code: 'fixture-code', state: 'fake-state' });
    assert.equal(mock.submitted(), true);
    assert.equal(mock.submissions(), 1);
  });
}

test('wrong callback state fails instead of returning a code', { timeout: 45000 }, async () => {
  const mock = fixture({ badState: true });
  await assert.rejects(authenticate(config, mock.setup), { reason: 'untrusted-navigation' });
  assert.equal(mock.submitted(), true);
});

test('MFA challenge fails without bypass or skip', { timeout: 45000 }, async () => {
  const mock = fixture({ challenge: true });
  await assert.rejects(authenticate(config, mock.setup), {
    phase: 'interactive challenge (MFA, CAPTCHA and consent are not bypassed)',
    reason: 'interactive-challenge',
  });
  assert.equal(mock.submitted(), true);
});


test('external form navigation still fails the login', { timeout: 45000 }, async () => {
  const mock = fixture({ externalForm: true });
  await assert.rejects(authenticate(config, mock.setup), { reason: 'untrusted-navigation' });
  assert.equal(mock.submitted(), false);
});


test('local Keycloak traffic uses the loopback relay and preserves the service Host', { timeout: 45000 }, async () => {
  let credentialRequests = 0;
  const relay = http.createServer(async (request, response) => {
    assert.equal(request.headers.host, 'keycloak.obco-lab-identity.svc:8080');
    if (request.method === 'GET') {
      response.writeHead(200, { 'Content-Type': 'text/html' });
      response.end('<form action="/login" method="post"><input name="username"><input name="password" type="password"><input type="submit" value="Sign In"></form>');
    } else {
      let body = '';
      for await (const chunk of request) body += chunk;
      const values = new URLSearchParams(body);
      assert.equal(values.get('username'), 'fixture-user');
      assert.equal(values.get('password'), config.password);
      credentialRequests += 1;
      response.writeHead(302, { location: 'http://127.0.0.1:8250/oidc/callback?code=fixture-code&state=fake-state' });
      response.end();
    }
  });
  await new Promise(resolve => relay.listen(0, '127.0.0.1', resolve));
  try {
    const result = await authenticate({ ...config, provider: 'keycloak',
      issuer: 'http://keycloak.obco-lab-identity.svc:8080/realms/obco-lab',
      url: 'http://keycloak.obco-lab-identity.svc:8080/realms/obco-lab/protocol/openid-connect/auth?state=fake-state',
      route_host: '127.0.0.1', route_port: relay.address().port });
    assert.deepEqual(result, { code: 'fixture-code', state: 'fake-state' });
    assert.equal(credentialRequests, 1);
  } finally {
    relay.closeAllConnections();
    await new Promise(resolve => relay.close(resolve));
  }
});

for (const [provider, issuer, field, realMFA = false] of [
  ['authentik', 'http://authentik.obco-lab-authentik.svc:9000/application/o/openbao/', 'uidField'],
  ['authentik', 'http://authentik.obco-lab-authentik.svc:9000/application/o/openbao/', 'uidField', true],
  ['zitadel', 'http://zitadel.obco-lab-zitadel.svc:8080', 'loginName'],
]) {
  test(`${provider} two-stage login ${realMFA ? 'rejects real MFA' : 'uses its input and preserves the service Host'}`, { timeout: 45000 }, async () => {
    let submissions = 0;
    const stageHTML = (tag, content) => provider !== 'authentik' ? content :
      `<${tag}></${tag}><script>document.querySelector('${tag}').attachShadow({mode:'open'}).innerHTML=${JSON.stringify(content)};</script>` +
      '<form><input name="username"><input type="password"><input autocomplete="one-time-code"></form><button type="button">Log in with passkey</button>';
    const relay = http.createServer(async (request, response) => {
      assert.equal(request.headers.host, new URL(issuer).host);
      if (request.method === 'GET') {
        if (request.url.includes('/authorize')) {
          response.writeHead(302, { location: new URL('/ui/login', issuer).toString(), 'Set-Cookie': 'fixture-session=bound; Path=/' });
          response.end();
          return;
        }
        assert.equal(request.headers.cookie, 'fixture-session=bound');
        response.writeHead(200, { 'Content-Type': 'text/html' });
        response.end(stageHTML('ak-stage-identification', `<form action="/password" method="post"><input name="${field}"><button type="submit">Continue</button></form>`));
        return;
      }
      let body = '';
      for await (const chunk of request) body += chunk;
      const values = new URLSearchParams(body);
      submissions += 1;
      if (request.url === '/password') {
        assert.equal(values.get(field), config.username);
        response.writeHead(200, { 'Content-Type': 'text/html' });
        response.end(stageHTML('ak-stage-password', '<form action="/complete" method="post"><input type="password" name="password"><button type="submit">Continue</button></form>'));
      } else {
        assert.equal(values.get('password'), config.password);
        if (realMFA) {
          response.writeHead(200, { 'Content-Type': 'text/html' });
          response.end(stageHTML('ak-stage-authenticator-validate', '<input autocomplete="one-time-code">'));
          return;
        }
        response.writeHead(302, { location: 'http://127.0.0.1:8250/oidc/callback?code=fixture-code&state=fake-state' });
        response.end();
      }
    });
    await new Promise(resolve => relay.listen(0, '127.0.0.1', resolve));
    try {
      const journey = authenticate({ ...config, provider, issuer,
        url: issuer + '/authorize?state=fake-state', route_host: '127.0.0.1', route_port: relay.address().port });
      if (realMFA) await assert.rejects(journey, { reason: 'interactive-challenge' });
      else assert.deepEqual(await journey, { code: 'fixture-code', state: 'fake-state' });
      assert.equal(submissions, 2);
    } finally {
      relay.closeAllConnections();
      await new Promise(resolve => relay.close(resolve));
    }
  });
}

// Private stdin/stdout carry credentials and callback codes; no screenshots, traces or URL logs.
import http from 'node:http';
import { createRequire } from 'node:module';
import { pathToFileURL } from 'node:url';
const require = createRequire(new URL('../browser/package.json', import.meta.url));
const CALLBACK = 'http://127.0.0.1:8250/oidc/callback';
const LOCAL_ISSUERS = {
  keycloak: 'http://keycloak.obco-lab-identity.svc:8080/realms/obco-lab',
  authentik: 'http://authentik.obco-lab-authentik.svc:9000/application/o/openbao/',
  zitadel: 'http://zitadel.obco-lab-zitadel.svc:8080',
};

export function validateConfig(config) {
  const issuer = new URL(config.issuer);
  const authorization = new URL(config.url);
  const local = Object.hasOwn(LOCAL_ISSUERS, config.provider) && config.issuer === LOCAL_ISSUERS[config.provider] &&
    ['127.0.0.1', 'host.docker.internal'].includes(config.route_host) && Number.isInteger(config.route_port) &&
    config.route_port > 0 && config.route_port <= 65535;
  if ((Object.hasOwn(LOCAL_ISSUERS, config.provider) && !local) || !['auth0', 'okta', ...Object.keys(LOCAL_ISSUERS)].includes(config.provider) || (issuer.protocol !== 'https:' && !local) ||
      authorization.protocol !== issuer.protocol || authorization.origin !== issuer.origin ||
      !config.username || !config.password || !config.state ||
      authorization.searchParams.get('state') !== config.state) {
    throw new Error('Invalid browser input');
  }
  return issuer;
}

export async function guardOutbound(route, issuerOrigin) {
  const request = route.request();
  const url = new URL(request.url());
  const navigation = request.isNavigationRequest();
  if (url.protocol !== 'https:' || ((navigation || !['GET', 'HEAD'].includes(request.method())) && url.origin !== issuerOrigin)) {
    await route.abort();
    return navigation && request.frame().parentFrame() === null;
  }
  await route.continue();
  return false;
}

export function localProxy(config, issuerOrigin) {
  // A browser proxy preserves redirected URLs, cookies and POST semantics. Route
  // interception alone misses Chromium's subsequent HTTP redirect requests.
  return http.createServer((request, response) => {
    let destination;
    try { destination = new URL(request.url); } catch { response.writeHead(400); response.end(); return; }
    if (destination.origin !== issuerOrigin || destination.protocol !== 'http:' || destination.username || destination.password) {
      response.writeHead(403); response.end(); return;
    }
    const headers = { ...request.headers, host: destination.host };
    delete headers['proxy-authorization'];
    delete headers['proxy-connection'];
    const upstream = http.request({ hostname: config.route_host, port: config.route_port,
      path: destination.pathname + destination.search, method: request.method, headers }, result => {
      if (result.statusCode >= 300 && result.statusCode < 400 && result.headers.location) {
        let target;
        try { target = new URL(result.headers.location, destination); } catch { /* reject below */ }
        const callbackTarget = target && target.origin === 'http://127.0.0.1:8250' && target.pathname === '/oidc/callback' &&
          target.searchParams.getAll('state').length === 1 && target.searchParams.get('state') === config.state &&
          target.searchParams.getAll('code').length === 1 && target.searchParams.get('code');
        const safeCallbackMethod = ['GET', 'HEAD'].includes(request.method) || result.statusCode === 303 ||
          (request.method === 'POST' && [301, 302].includes(result.statusCode));
        if (!target || target.username || target.password ||
            (target.origin !== issuerOrigin && !(callbackTarget && safeCallbackMethod))) {
          result.resume(); response.writeHead(403); response.end(); return;
        }
      }
      response.writeHead(result.statusCode, result.headers);
      result.pipe(response);
    });
    upstream.setTimeout(30000, () => upstream.destroy(new Error('Local provider timeout')));
    upstream.on('error', () => {
      if (!response.destroyed) { if (!response.headersSent) response.writeHead(502); response.end(); }
    });
    request.on('aborted', () => upstream.destroy());
    response.on('close', () => upstream.destroy());
    request.pipe(upstream);
  }).on('connect', (_request, socket) => socket.destroy());
}

export async function authenticate(config, setupContext = async () => {}) {
  const issuer = validateConfig(config);
  const { chromium } = require('playwright');
  let browser;
  let proxyServer;
  let stage = 'launch Chromium';
  let reason = 'browser-error';
  let callback;
  let rejectedNavigation = false;
  const callbackServer = http.createServer((request, response) => {
    const url = new URL(request.url, 'http://127.0.0.1:8250');
    const valid = url.pathname === '/oidc/callback' && url.searchParams.getAll('state').length === 1 &&
      url.searchParams.get('state') === config.state && url.searchParams.getAll('code').length === 1 && url.searchParams.get('code');
    if (valid) callback = { state: config.state, code: url.searchParams.get('code') };
    else rejectedNavigation = true;
    response.writeHead(valid ? 200 : 400, { 'Content-Type': 'text/plain', 'Cache-Control': 'no-store' });
    response.end('Return to the test runner');
  });
  try {
    await new Promise((resolve, reject) => {
      callbackServer.once('error', reject);
      callbackServer.listen(8250, '127.0.0.1', resolve);
    });
    const launch = { headless: true, timeout: 30000 };
    if (Object.hasOwn(LOCAL_ISSUERS, config.provider)) {
      proxyServer = localProxy(config, issuer.origin);
      await new Promise((resolve, reject) => {
        proxyServer.once('error', reject);
        proxyServer.listen(0, '127.0.0.1', resolve);
      });
      launch.proxy = { server: `http://127.0.0.1:${proxyServer.address().port}`, bypass: '127.0.0.1' };
    }
    browser = await chromium.launch(launch);
    const context = await browser.newContext({ serviceWorkers: 'block' });
    const page = await context.newPage();
    page.setDefaultTimeout(15000);
    await context.route('**/*', async route => {
      const request = route.request();
      const url = new URL(request.url());
      if (url.origin === 'http://127.0.0.1:8250' && url.pathname === '/oidc/callback') {
        if (url.searchParams.getAll('state').length === 1 && url.searchParams.get('state') === config.state &&
            url.searchParams.getAll('code').length === 1 && url.searchParams.get('code')) {
          callback = { state: config.state, code: url.searchParams.get('code') };
        } else {
          rejectedNavigation = true;
        }
        await route.fulfill({ status: 200, contentType: 'text/plain', body: 'Return to the test runner' });
      } else if (Object.hasOwn(LOCAL_ISSUERS, config.provider) && url.origin === issuer.origin) {
        await route.continue();
      } else if (await guardOutbound(route, issuer.origin)) {
        rejectedNavigation = true;
      }
    });
    await setupContext(context);
    stage = 'load hosted login';
    await page.goto(config.url, { waitUntil: 'domcontentloaded' });
    const deadline = Date.now() + 150000;
    let submissions = 0;
    const submittedForms = new Set();
    while (!callback && Date.now() < deadline) {
      if (rejectedNavigation) { reason = 'untrusted-navigation'; throw new Error('Unexpected navigation'); }
      const current = new URL(page.url());
      if (current.origin !== issuer.origin && page.url().split('?')[0] !== CALLBACK) { reason = 'untrusted-navigation'; throw new Error('Unexpected page'); }
      // Authentik adds an off-stage password-manager helper with OTP/password
      // inputs. Inspect its actual stages rather than mistaking that helper for MFA.
      const challengeSelector = config.provider === 'authentik'
        ? 'ak-stage-authenticator-validate, ak-stage-authenticator-totp, ak-stage-authenticator-webauthn, ak-stage-captcha, iframe[src*="captcha"]'
        : 'input[autocomplete="one-time-code"], iframe[src*="captcha"], input[name="credentials.answer"]';
      const challenge = page.locator(challengeSelector).filter({ visible: true });
      if (await challenge.count() > 0) {
        stage = 'interactive challenge (MFA, CAPTCHA and consent are not bypassed)';
        reason = 'interactive-challenge';
        throw new Error('Interactive challenge requires account setup');
      }
      const loginForm = config.provider === 'authentik' ? page.locator('ak-stage-identification, ak-stage-password') : page;
      const password = loginForm.locator('input[type="password"]').filter({ visible: true }).first();
      const providerUsername = config.provider === 'authentik' ? ', input[name="uidField"]' : config.provider === 'zitadel' ? ', input[name="loginName"]' : '';
      const username = loginForm.locator('input[name="username"], input[name="identifier"], input[autocomplete="username"], input[type="email"]' + providerUsername).filter({ visible: true }).first();
      const hasPassword = await password.count() > 0;
      const hasUsername = await username.count() > 0;
      if (hasPassword || hasUsername) {
        const formKey = current.pathname + (hasPassword ? ':password' : ':username');
        if (submittedForms.has(formKey)) {
          const errors = page.locator('[role="alert"], .o-form-error-container, [data-error-code]').filter({ visible: true });
          if (await errors.count() > 0 && (await errors.allTextContents()).some(text => text.trim())) {
            reason = 'provider-error';
            throw new Error('Provider rejected the login');
          }
          await page.waitForTimeout(250);
          continue;
        }
        if (submissions >= 3) { reason = 'repeated-form'; throw new Error('Hosted login did not complete'); }
        stage = hasPassword ? 'submit password' : 'submit username';
        if (hasUsername) await username.fill(config.username);
        if (hasPassword) await password.fill(config.password);
        let submit = loginForm.locator('button[type="submit"], input[type="submit"]').filter({ visible: true }).first();
        if (await submit.count() === 0) {
          submit = loginForm.getByRole('button', { name: /^(continue|next|sign in|log in|verify)$/i }).first();
        }
        submittedForms.add(formKey);
        await submit.click();
        submissions += 1;
        // Hosted widgets may update in place before changing the visible form.
        await page.waitForTimeout(1000);
      } else {
        stage = 'await hosted callback (MFA, CAPTCHA and consent are not bypassed)';
        await page.waitForTimeout(250);
      }
    }
    if (!callback) { reason = 'callback-timeout'; throw new Error('Hosted login timed out'); }
    return callback;
  } catch (error) {
    const failure = new Error('Hosted browser login failed at ' + stage + '; check the dedicated test application policy, credentials, MFA/CAPTCHA or consent requirements');
    failure.phase = stage;
    failure.reason = error.name === 'TimeoutError' ? 'timeout' : reason;
    throw failure;
  } finally {
    if (browser) await browser.close();
    if (proxyServer?.listening) {
      proxyServer.closeAllConnections();
      await new Promise(resolve => proxyServer.close(resolve));
    }
    if (callbackServer.listening) await new Promise(resolve => callbackServer.close(resolve));
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    let input = '';
    for await (const chunk of process.stdin) input += chunk;
    const callback = await authenticate(JSON.parse(input));
    process.stdout.write(JSON.stringify(callback));
  } catch (error) {
    const phase = error.phase || 'runner setup';
    const reason = error.reason || 'invalid-input-or-runner';
    process.stderr.write(JSON.stringify({ phase, reason }) + '\n');
    process.exitCode = 1;
  }
}

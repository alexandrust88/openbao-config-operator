// Credentials and callback codes travel over private subprocess pipes, never logs.
import { chromium } from 'playwright';
import http from 'node:http';
let input = '';
for await (const chunk of process.stdin) input += chunk;
const config = JSON.parse(input);
const callbackServer = http.createServer((request, response) => {
  const url = new URL(request.url, 'http://localhost:8250');
  response.writeHead(url.pathname === '/oidc/callback' ? 200 : 404, { 'Content-Type': 'text/plain' });
  response.end(url.pathname === '/oidc/callback' ? 'OIDC callback received' : 'Not found');
});
let stage = 'start callback server';
let browser;
let page;
try {
  await new Promise((resolve, reject) => {
    callbackServer.once('error', reject);
    callbackServer.listen(8250, '127.0.0.1', resolve);
  });
  stage = 'launch Chromium';
  browser = await chromium.launch({ headless: true, timeout: 30000 });
  const context = await browser.newContext();
  page = await context.newPage();
  page.setDefaultTimeout(30000);
  const issuer = new URL(config.issuer);
  await page.route('**/*', async route => {
    const url = new URL(route.request().url());
    if (url.origin === 'http://localhost:8250' && url.pathname === '/oidc/callback') {
      await route.fulfill({ status: 200, contentType: 'text/plain', body: 'OIDC callback received' });
    } else if (url.origin === issuer.origin) {
      const originalHost = url.host;
      url.hostname = config.host;
      url.port = String(config.port);
      const response = await route.fetch({ url: url.toString(), headers: { ...route.request().headers(), host: originalHost }, maxRedirects: 0 });
      await route.fulfill({ response });
    } else {
      await route.abort();
    }
  });
  stage = 'load login page';
  await page.goto(config.url);
  await page.screenshot({ path: config.artifacts + '/login.png' });
  stage = 'fill username';
  await page.locator('#username').fill(config.username);
  await page.locator('#password').fill(config.password);
  stage = 'submit login and await callback';
  await Promise.all([
    page.waitForURL(url => url.origin === 'http://localhost:8250' && url.pathname === '/oidc/callback'),
    page.locator('#kc-login').click(),
  ]);
  const callback = new URL(page.url());
  const code = callback.searchParams.get('code');
  const state = callback.searchParams.get('state');
  if (!code || !state) throw new Error('Callback omitted code or state');
  process.stdout.write(JSON.stringify({ code, state }));
} catch {
  if (page) await page.screenshot({ path: config.artifacts + '/failure.png', mask: [page.locator('#password')] }).catch(() => {});
  process.stderr.write('Browser OIDC failed at ' + stage + ' (credentials and URLs redacted)\n');
  process.exitCode = 1;
} finally {
  if (browser) await browser.close();
  if (callbackServer.listening) await new Promise(resolve => callbackServer.close(resolve));
}

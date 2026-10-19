// Renders the app's PNG icons (web/public/icons) from icon.svg and
// icon-maskable.svg with a Chromium: node hack/render-icons.cjs web/public/icons
// (needs Playwright; PLAYWRIGHT_CHROMIUM or the bundled browser).
const { chromium } = require('playwright');
const fs = require('fs');
(async () => {
  const dir = process.argv[2];
  const b = await chromium.launch({ executablePath: process.env.PLAYWRIGHT_CHROMIUM || undefined });
  const p = await b.newPage();
  const render = async (svg, size, out, bg) => {
    const data = fs.readFileSync(`${dir}/${svg}`, 'utf8');
    await p.setViewportSize({ width: size, height: size });
    await p.setContent(`<html><body style="margin:0;background:${bg ?? 'transparent'}">${data.replace('<svg ', `<svg width="${size}" height="${size}" `)}</body></html>`);
    await p.screenshot({ path: `${dir}/${out}`, omitBackground: !bg, clip: { x: 0, y: 0, width: size, height: size } });
  };
  await render('icon.svg', 192, 'icon-192.png');
  await render('icon.svg', 512, 'icon-512.png');
  await render('icon-maskable.svg', 512, 'icon-maskable-512.png');
  await render('icon-maskable.svg', 192, 'icon-maskable-192.png');
  // iOS: square, no transparency (it rounds the corners itself).
  await render('icon-maskable.svg', 180, 'apple-touch-icon.png', '#3b5bdb');
  await b.close();
})();

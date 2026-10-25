// Checks every relative link and anchor in the built site, which
// starlight-links-validator leaves alone: pages link relatively so the
// site works under any base path, a custom domain's included.
import { readFileSync, readdirSync, existsSync, statSync } from 'node:fs';
import { join, dirname, resolve } from 'node:path';

const dist = resolve('dist');
const pages = [];
(function walk(dir) {
	for (const name of readdirSync(dir)) {
		const p = join(dir, name);
		if (statSync(p).isDirectory()) walk(p);
		else if (name.endsWith('.html')) pages.push(p);
	}
})(dist);

const ids = new Map();
const idsOf = (file) => {
	if (!ids.has(file)) {
		const html = readFileSync(file, 'utf8');
		ids.set(file, new Set([...html.matchAll(/\sid="([^"]+)"/g)].map((m) => m[1])));
	}
	return ids.get(file);
};

let broken = 0;
for (const page of pages) {
	const html = readFileSync(page, 'utf8');
	for (const [, raw] of html.matchAll(/<a\s[^>]*href="([^"]+)"/g)) {
		const href = raw.replaceAll('&amp;', '&');
		if (/^[a-z]+:|^\/\//i.test(href) || href.startsWith('/')) continue;
		const [path, hash] = href.split('#');
		let target = path ? resolve(dirname(page), path) : page;
		if (path && (path.endsWith('/') || !existsSync(target) || statSync(target).isDirectory())) {
			target = join(target, 'index.html');
		}
		const where = page.slice(dist.length);
		if (!existsSync(target)) {
			console.error(`${where}: no page for ${href}`);
			broken++;
		} else if (hash && !idsOf(target).has(decodeURIComponent(hash))) {
			console.error(`${where}: no #${hash} in ${href}`);
			broken++;
		}
	}
}
if (broken) {
	console.error(`${broken} broken link(s)`);
	process.exit(1);
}
console.log(`All relative links in ${pages.length} pages are valid.`);

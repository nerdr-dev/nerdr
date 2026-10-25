import { getImage } from 'astro:assets';
import { defineRouteMiddleware } from '@astrojs/starlight/route-data';
import logo from './assets/nerdr-glasses.png';

// Starlight's hero has no place for a logo, so the home page's (the entry
// with the empty id) goes into the title, which the hero renders as HTML.
// The title beside it names it, so its alt text is empty.
export const onRequest = defineRouteMiddleware(async (context) => {
	const { entry } = context.locals.starlightRoute;
	if (entry.id !== '' || !entry.data.hero) return;
	const image = await getImage({ src: logo, width: 192 });
	entry.data.hero.title =
		`<img class="hero-logo" src="${image.src}" width="96" height="96" alt="">` +
		(entry.data.hero.title ?? entry.data.title);
});

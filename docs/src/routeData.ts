import { getImage } from 'astro:assets';
import { defineRouteMiddleware } from '@astrojs/starlight/route-data';
import logo from './assets/nerdr-glasses.png';

// Starlight's hero has no slot between the title and the tagline, so the
// home page's logo (the entry with the empty id) goes in at the start of
// the tagline.
export const onRequest = defineRouteMiddleware(async (context) => {
	const { hero } = context.locals.starlightRoute.entry.data;
	if (context.locals.starlightRoute.entry.id !== '' || !hero?.tagline) return;
	const image = await getImage({ src: logo, width: 192 });
	hero.tagline =
		`<img class="hero-logo" src="${image.src}" width="96" height="96" ` +
		`alt="The nerdr logo: a pair of glasses whose lenses are a yellow and a green status light">` +
		hero.tagline;
});

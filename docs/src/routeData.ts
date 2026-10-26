import { getImage } from 'astro:assets';
import { defineRouteMiddleware } from '@astrojs/starlight/route-data';
import logo from './assets/nerdr-glasses.png';

// Starlight titles every page "<page> | <site>", which on the home page (the
// entry with the empty id) reads "nerdr | nerdr", so its title is just the
// site's name.
// Starlight's hero has no place for a logo, so the home page's goes into the
// title, which the hero renders as HTML. The title beside it names it, so its
// alt text is empty.
export const onRequest = defineRouteMiddleware(async (context) => {
	const route = context.locals.starlightRoute;
	const { entry } = route;
	if (entry.id !== '') return;
	const title = route.head.find((item) => item.tag === 'title');
	if (title) title.content = route.siteTitle;
	if (!entry.data.hero) return;
	const image = await getImage({ src: logo, width: 192 });
	entry.data.hero.title =
		`<img class="hero-logo" src="${image.src}" width="96" height="96" alt="">` +
		(entry.data.hero.title ?? entry.data.title);
});

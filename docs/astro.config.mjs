// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import starlightLinksValidator from 'starlight-links-validator';

// The Pages workflow passes the site's origin and base path, so a custom
// domain set in the repository's Pages settings needs no change here.
// Without them, as in a pull request or locally, the github.io address.
const site = process.env.SITE || 'https://nerdr-dev.github.io';
const base = process.env.SITE ? process.env.BASE || '/' : '/nerdr';

export default defineConfig({
	site,
	base,
	trailingSlash: 'always',
	integrations: [
		starlight({
			title: 'nerdr',
			description:
				'Run several coding agents at once, each in its own Git worktree, and review their work in one terminal app.',
			favicon: '/favicon.svg',
			customCss: ['./src/styles/nerdr.css'],
			routeMiddleware: './src/routeData.ts',
			social: [{ icon: 'github', label: 'GitHub', href: 'https://github.com/nerdr-dev/nerdr' }],
			editLink: { baseUrl: 'https://github.com/nerdr-dev/nerdr/edit/main/docs/' },
			lastUpdated: true,
			plugins: [starlightLinksValidator({ errorOnRelativeLinks: false })],
			sidebar: [
				{
					label: 'Start here',
					items: ['start/about', 'start/install', 'start/quick-start', 'start/concepts'],
				},
				{
					label: 'Using nerdr',
					items: [
						'use/tabs',
						'use/workbench',
						'use/agents-together',
						'use/code-review',
						'use/progress',
						'use/remote',
					],
				},
				{
					label: 'The review panel',
					items: [
						'panel/overview',
						'panel/code-changes',
						'panel/github',
						'panel/tasks',
						'panel/skills',
						'panel/plugins',
					],
				},
				{
					label: 'Configure',
					items: ['configure/configuration', 'configure/keyboard', 'configure/agents', 'configure/plugins'],
				},
				{
					label: 'Reference',
					items: [
						'reference/agent-commands',
						'reference/security',
						'reference/how-it-works',
						'reference/troubleshooting',
						'reference/development',
					],
				},
			],
		}),
	],
});

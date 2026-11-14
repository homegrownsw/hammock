// Hammock's docs site: Starlight, served at homegrownsw.com/hammock/docs/. `just docs` builds it
// (generating the reference pages first), `just docs-dev` previews it; site/README.md says more.
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import docsComments from './dev/comments.mjs';

export default defineConfig({
	site: 'https://homegrownsw.com',
	base: '/hammock/docs',
	trailingSlash: 'always',
	integrations: [
		// Review comments, in `just docs-dev` only (dev/comments.mjs).
		docsComments(),
		starlight({
			title: 'Hammock',
			description: 'Docs for Hammock: a native Mac terminal that never loses your work, built for coding agents.',
			logo: { src: './src/assets/icon.png', alt: '' },
			favicon: '/favicon.png',
			customCss: ['./src/styles/hammock.css'],
			components: { Footer: './src/components/Footer.astro' },
			head: [
				{ tag: 'link', attrs: { rel: 'preconnect', href: 'https://fonts.googleapis.com' } },
				{ tag: 'link', attrs: { rel: 'preconnect', href: 'https://fonts.gstatic.com', crossorigin: '' } },
				{
					tag: 'link',
					attrs: {
						rel: 'stylesheet',
						href: 'https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600&family=JetBrains+Mono:wght@400;500&family=Space+Grotesk:wght@500;600;700&display=swap',
					},
				},
			],
			social: [
				{ icon: 'github', label: 'GitHub', href: 'https://github.com/homegrownsw/hammock' },
			],
			editLink: { baseUrl: 'https://github.com/homegrownsw/hammock/edit/main/site/' },
			lastUpdated: false,
			sidebar: [
				// Generated from release-notes/ (scripts/docsgen), so it's always the latest.
				{ label: "What's new", link: '/whats-new/' },
				{
					label: 'Start here',
					items: [
						{ label: 'Introduction', link: '/' },
						'status',
						'install/mac',
						'install/iphone',
						'install/linux',
						'permissions',
					],
				},
				{
					label: 'Using Hammock',
					items: [
						'workspace',
						'panes',
						'tui',
						'scheduled-tasks',
						'remote',
						'screen-sharing',
						'iphone',
					],
				},
				{ label: 'Configuration', items: ['configuration'] },
				{
					label: 'Agents',
					items: ['agents/overview', 'agents/setup', 'agents/mcp', 'agents/examples'],
				},
				{ label: 'The CLI', items: ['cli'] },
				{ label: 'Reference', items: [{ autogenerate: { directory: 'reference' } }] },
				{ label: 'Help', items: ['troubleshooting', 'reporting-issues'] },
			],
		}),
	],
});

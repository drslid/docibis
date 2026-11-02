// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';

export default defineConfig({
	site: 'https://drslid.github.io',
	base: '/docibis',
	integrations: [
		starlight({
			title: 'DocIbis',
			description: 'Send a Word template and JSON data, get a finished PDF back in one API call.',
			logo: { src: './src/assets/logo.svg', replacesTitle: true },
			favicon: '/favicon.svg',
			customCss: ['./src/styles/theme.css'],
			social: [{ icon: 'github', label: 'GitHub', href: 'https://github.com/drslid/docibis' }],
			editLink: { baseUrl: 'https://github.com/drslid/docibis/edit/main/apps/docs/' },
			sidebar: [
				{
					label: 'Getting started',
					items: [
						{ label: 'Install', slug: 'getting-started/install' },
						{ label: 'First steps', slug: 'getting-started/first-steps' },
					],
				},
				{
					label: 'Guides',
					items: [
						{ label: 'Write a template', slug: 'guides/templates' },
						{ label: 'Produce documents by API', slug: 'guides/api' },
					],
				},
				{
					label: 'Run DocIbis',
					items: [
						{ label: 'Configuration', slug: 'operations/configuration' },
						{ label: 'Day-to-day operations', slug: 'operations/maintenance' },
					],
				},
			],
		}),
	],
});

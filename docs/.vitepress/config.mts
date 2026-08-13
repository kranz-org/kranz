import { defineConfig } from 'vitepress'

export default defineConfig({
  title: 'Kranz',
  description: 'A keyboard-first local service orchestrator with a focused terminal UI.',
  base: '/kranz/',
  cleanUrls: true,
  lastUpdated: true,
  head: [
    ['meta', { name: 'theme-color', content: '#0b0f15' }],
    ['link', { rel: 'icon', href: '/kranz/favicon.svg', type: 'image/svg+xml' }]
  ],
  themeConfig: {
    // The header uses a compact variant whose hub is large enough to read at
    // navigation size; the full mark stays on the home page and in the README.
    logo: {
      light: '/logo-mark-light.svg',
      dark: '/logo-mark.svg',
      alt: 'Kranz'
    },
    siteTitle: 'Kranz',
    nav: [
      { text: 'Guide', link: '/guide/getting-started' },
      { text: 'Reference', link: '/reference/configuration' },
      { text: 'Examples', link: '/examples' },
      { text: 'Releases', link: 'https://github.com/kranz-org/kranz/releases' }
    ],
    sidebar: [
      {
        text: 'Guide',
        items: [
          { text: 'What is Kranz?', link: '/guide/what-is-kranz' },
          { text: 'Getting started', link: '/guide/getting-started' },
          { text: 'Core concepts', link: '/guide/core-concepts' },
          { text: 'Installation', link: '/guide/installation' },
          { text: 'Configuration', link: '/guide/configuration' },
          { text: 'Lifecycle', link: '/guide/lifecycle' },
          { text: 'Actions', link: '/guide/actions' },
          { text: 'Health and dependencies', link: '/guide/health-and-dependencies' },
          { text: 'Logs and ports', link: '/guide/logs-and-ports' },
          { text: 'Appearance', link: '/guide/appearance' },
          { text: 'Troubleshooting', link: '/guide/troubleshooting' }
        ]
      },
      {
        text: 'Reference',
        items: [
          { text: 'Configuration reference', link: '/reference/configuration' },
          { text: 'Annotated kranz.yaml', link: '/reference/kranz-yaml' },
          { text: 'CLI', link: '/reference/cli' },
          { text: 'Controls', link: '/reference/controls' },
          { text: 'Process Compose', link: '/reference/process-compose' }
        ]
      },
      {
        text: 'Runnable examples',
        items: [
          { text: 'Choose an example', link: '/examples' },
          { text: 'MoonFlight showcase', link: '/examples/moonflight' },
          { text: 'Procfile quickstart', link: '/examples/procfile' },
          { text: 'Native YAML', link: '/examples/native' },
          { text: 'Detached lifecycle', link: '/examples/lifecycle' },
          { text: 'Prerequisites', link: '/examples/prerequisites' },
          { text: 'Process Compose', link: '/examples/process-compose' },
          { text: 'Full dependency graph', link: '/examples/full-stack' },
          { text: 'Runtime ports', link: '/examples/runtime-ports' }
        ]
      }
    ],
    socialLinks: [
      { icon: 'github', link: 'https://github.com/kranz-org/kranz' }
    ],
    search: { provider: 'local' },
    footer: {
      message: 'Released under the MIT License.',
      copyright: 'Copyright © Kranz contributors'
    },
    editLink: {
      pattern: 'https://github.com/kranz-org/kranz/edit/main/docs/:path',
      text: 'Edit this page on GitHub'
    }
  }
})

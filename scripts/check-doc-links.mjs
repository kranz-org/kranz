import { existsSync, readdirSync, readFileSync } from 'node:fs'
import { dirname, extname, join, relative, resolve } from 'node:path'

const repositoryRoot = resolve(import.meta.dirname, '..')
const markdownFiles = [
  join(repositoryRoot, 'README.md'),
  ...walk(join(repositoryRoot, 'docs')),
]
const failures = []

for (const file of markdownFiles) {
  const source = readFileSync(file, 'utf8')

  for (const match of source.matchAll(/!?\[[^\]]*\]\(([^)\s]+)(?:\s+"[^"]*")?\)/g)) {
    const rawTarget = match[1]
    if (/^(?:https?:|mailto:|#)/.test(rawTarget)) continue

    const target = decodeURIComponent(rawTarget.split('#', 1)[0].split('?', 1)[0])
    if (target === '') continue

    if (!resolvesToFile(dirname(file), target)) {
      failures.push(`${relative(repositoryRoot, file)} -> ${rawTarget}`)
    }
  }
}

if (failures.length > 0) {
  console.error('Broken local documentation links:')
  for (const failure of failures) console.error(`- ${failure}`)
  process.exit(1)
}

console.log(`Checked ${markdownFiles.length} Markdown files.`)

function walk(directory) {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name)
    if (entry.isDirectory()) return walk(path)
    return extname(entry.name) === '.md' ? [path] : []
  })
}

function resolvesToFile(sourceDirectory, target) {
  const path = resolve(sourceDirectory, target)
  if (existsSync(path)) return true
  if (existsSync(`${path}.md`)) return true
  return existsSync(join(path, 'index.md'))
}

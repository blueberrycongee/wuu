import { readFileSync, readdirSync, realpathSync } from 'node:fs';
import { resolve } from 'node:path';
import type { Plugin } from 'vite';

// Remove these version-pinned supplements when upstream npm tarballs ship licenses.
// Exact release-commit source URLs are recorded beside the unmodified texts.
const licenseSupplements: Record<string, string> = {
  'remark-math@6.0.0': 'remark-math-MIT.txt',
  'rehype-katex@7.0.1': 'remark-math-MIT.txt',
  'cordis@4.0.0-rc.8': 'cordis-MIT.txt',
};

/** Keep dependency notices in the renderer output when packaging prunes devDependencies. */
export function bundledDependencyLicenses(desktopRoot: string): Plugin {
  return {
    name: 'wuu-bundled-dependency-licenses',
    apply: 'build',
    generateBundle() {
      // Vite resolves the linked blobatar package to vendor/, outside node_modules.
      // Zeron's copied sources have no package.json but still require attribution.
      const zeronRoot = realpathSync(resolve(desktopRoot, 'vendor/zeron'));
      const roots = new Set<string>([
        realpathSync(resolve(desktopRoot, 'vendor/blobatar')),
        zeronRoot,
      ]);
      for (const id of this.getModuleIds()) {
        if (id.startsWith('\0')) continue;
        // The last node_modules segment identifies nested installs as well as scopes.
        const match = id.split('?')[0].replaceAll('\\', '/').match(/^(.*\/node_modules\/(?:@[^/]+\/)?[^/]+)(?:\/|$)/);
        if (match) roots.add(realpathSync(match[1]));
      }

      const sections = [...roots].map((root) => {
        const isZeron = root === zeronRoot;
        const metadata = isZeron ? { name: 'zeron', version: 'vendored' }
          : JSON.parse(readFileSync(resolve(root, 'package.json'), 'utf8')) as { name: string; version: string };
        const identity = `${metadata.name}@${metadata.version}`;
        const files = readdirSync(root, { withFileTypes: true })
          .filter((entry) => entry.isFile() && /^(?:licen[cs]e|copying|notice|copyright|third[-_]?party[-_]?notices?)/i.test(entry.name))
          .map((entry) => entry.name).sort();
        const licenses = files.filter((name) => /^(?:licen[cs]e|copying)/i.test(name));
        const supplement = !licenses.length && licenseSupplements[identity];
        const supplementText = supplement ? readFileSync(resolve(desktopRoot, 'vendor/licenses', supplement), 'utf8').trim() : '';
        if ((!licenses.length && !supplementText) || licenses.some((name) => !readFileSync(resolve(root, name), 'utf8').trim())) {
          this.error(`Missing license text for ${identity}; restore the dependency's LICENSE/COPYING file before releasing.`);
        }
        return {
          identity,
          text: [identity, ...(supplementText ? [`\n--- LICENSE (upstream release) ---\n${supplementText}`] : []), ...files.map((name) => `\n--- ${name} ---\n${readFileSync(resolve(root, name), 'utf8').trim()}`)].join('\n'),
        };
      }).sort((a, b) => a.identity.localeCompare(b.identity) || a.text.localeCompare(b.text));
      this.emitFile({
        type: 'asset',
        fileName: 'THIRD-PARTY-LICENSES.txt',
        source: `Third-party software notices\n\n${sections.map(({ text }) => text).join('\n\n========================================\n\n')}\n`,
      });
    },
  };
}

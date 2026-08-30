import { build } from 'esbuild';
import { cp, mkdir, rm } from 'node:fs/promises';

// The build: four bundles and two static files into dist/, which is what
// `chrome://extensions` loads unpacked and what the store takes zipped.
//
// IIFE rather than ESM, for every one of them. A content script cannot be a module at
// all, and a service worker can only be one if the manifest says so — one format for
// the four is one thing to be wrong about instead of two.
//
// No minification and no name mangling. This is a security control somebody may want
// to read before trusting it with their prompts, and a reviewer at the Web Store has
// to read it too; a bundle nobody can read is a claim rather than a control.

const entries = [
  'src/background.ts',
  'src/relay.ts',
  'src/interceptor.ts',
  'src/options.ts',
];

await rm('dist', { recursive: true, force: true });
await mkdir('dist', { recursive: true });

await build({
  entryPoints: entries,
  outdir: 'dist',
  bundle: true,
  format: 'iife',
  target: 'chrome111', // the first version with content_scripts[].world
  platform: 'browser',
  logLevel: 'info',
});

await cp('src/manifest.json', 'dist/manifest.json');
await cp('src/options.html', 'dist/options.html');

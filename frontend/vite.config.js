import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'

// Everything is inlined into a single HTML file: the Wails asset server serves the
// embedded dist directory, and a one-file bundle keeps the embed trivial.
export default defineConfig({
  plugins: [svelte()],
  build: {
    // Emitted next to the Go main package so it can be embedded directly.
    outDir: '../cmd/sa05/dist',
    emptyOutDir: true,
    target: 'es2022',
    assetsInlineLimit: 1024 * 1024,
    cssCodeSplit: false,
    rollupOptions: {
      output: {
        inlineDynamicImports: true,
        entryFileNames: 'app.js',
        assetFileNames: 'app.[ext]',
      },
    },
  },
})

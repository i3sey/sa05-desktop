import { mount } from 'svelte'
import './app.css'
import App from './App.svelte'

const target = document.getElementById('app')
if (!target) throw new Error('нет контейнера #app')

// First paint before the backend View arrives: reuse the last theme so a dark-mode
// user does not get flashed with light. App.svelte overwrites it from View.theme.
try {
  const cached = localStorage.getItem('sa05-theme')
  if (cached === 'light' || cached === 'dark' || cached === 'auto') {
    document.documentElement.dataset.theme = cached
  }
} catch {
  /* private mode: CSS falls back to the OS scheme */
}

export default mount(App, { target })

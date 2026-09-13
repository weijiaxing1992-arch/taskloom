<script setup lang="ts">
defineProps<{ name: string, size?: number }>()
const paths: Record<string, string[]> = {
  menu: ['M4 6h16','M4 12h16','M4 18h16'],
  chart: ['M3 3v18h18','M7 16V9','M12 16V5','M17 16v-4'],
  plus: ['M12 5v14', 'M5 12h14'],
  tag: ['M3 3h8l10 10-8 8L3 11z', 'M7.5 7.5h.01'],
  weights: ['M4 20h16', 'M6 16V9', 'M12 16V4', 'M18 16v-5'],
  link: ['M10 13a5 5 0 0 0 7 0l3-3a5 5 0 0 0-7-7l-2 2', 'M14 11a5 5 0 0 0-7 0l-3 3a5 5 0 0 0 7 7l2-2'],
  folder: ['M3 7V5h6l2 2h10v13H3z'],
  more: ['M5 12h.01', 'M12 12h.01', 'M19 12h.01'],
  edit: ['M16 3l5 5-12 12H4v-5z', 'M13 6l5 5'],
  trash: ['M3 6h18', 'M8 6V3h8v3', 'M5 6l1 15h12l1-15', 'M10 10v7', 'M14 10v7'],
  list: ['M8 6h13', 'M8 12h13', 'M8 18h13', 'M3 6h.01', 'M3 12h.01', 'M3 18h.01'],
  review: ['M4 3h16v18H4z', 'M8 7h8', 'M8 12h8', 'M8 17h4'],
  projects: ['M3 5h18v14H3z', 'M3 10h18', 'M9 5v14'],
  work: ['M4 12l5 5L20 6'],
  search: ['M11 18a7 7 0 1 1 0-14 7 7 0 0 1 0 14z', 'M16 16l5 5'],
  bell: ['M18 8a6 6 0 0 0-12 0c0 7-3 7-3 9h18c0-2-3-2-3-9', 'M10 21h4'],
  mobile: ['M8 3h8a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2z', 'M11 18h2'],
  settings: ['M12 15.5a3.5 3.5 0 1 0 0-7 3.5 3.5 0 0 0 0 7z', 'M19.4 15a1.7 1.7 0 0 0 .3 1.9l.1.1-2.1 2.1-.1-.1a1.7 1.7 0 0 0-1.9-.3 1.7 1.7 0 0 0-1 1.6v.2h-3v-.2a1.7 1.7 0 0 0-1-1.6 1.7 1.7 0 0 0-1.9.3l-.1.1L6.6 17l.1-.1A1.7 1.7 0 0 0 7 15a1.7 1.7 0 0 0-1.6-1H5v-3h.4A1.7 1.7 0 0 0 7 10a1.7 1.7 0 0 0-.3-1.9L6.6 8l2.1-2.1.1.1a1.7 1.7 0 0 0 1.9.3 1.7 1.7 0 0 0 1-1.6v-.2h3v.2a1.7 1.7 0 0 0 1 1.6 1.7 1.7 0 0 0 1.9-.3l.1-.1L19.8 8l-.1.1a1.7 1.7 0 0 0-.3 1.9 1.7 1.7 0 0 0 1.6 1h.4v3H21a1.7 1.7 0 0 0-1.6 1z'],
  fields: ['M4 5h16v4H4z', 'M4 15h16v4H4z', 'M8 9v6', 'M16 9v6'],
  help: ['M9.4 9a3 3 0 1 1 4.7 2.5c-1.2.8-2.1 1.3-2.1 2.5', 'M12 18h.01', 'M12 22a10 10 0 1 0 0-20 10 10 0 0 0 0 20z'],
  user: ['M20 21a8 8 0 0 0-16 0', 'M12 13a5 5 0 1 0 0-10 5 5 0 0 0 0 10z'],
  shield: ['M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z', 'M9 12l2 2 4-4'],
  preferences: ['M4 6h10', 'M18 6h2', 'M4 12h2', 'M10 12h10', 'M4 18h7', 'M15 18h5', 'M14 4v4', 'M6 10v4', 'M11 16v4'],
  key: ['M21 2l-2 2', 'M17 6l-2 2', 'M14 5a7 7 0 1 1-3-3 7 7 0 0 1 3 3z', 'M7 9h.01'],
}
</script>

<template><svg class="svg-icon" :width="size || 16" :height="size || 16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path v-for="path in paths[name] || paths.help" :key="path" :d="path" /></svg></template>

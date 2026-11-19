import { createApp } from 'vue';
import App from './App.vue';
import LibraryPanel from './LibraryPanel.vue';
import SettingsPanel from './SettingsPanel.vue';
import './style.css';
import './hacker.css';

createApp(App, { role: 'agent', libraryPanel: LibraryPanel, settingsPanel: SettingsPanel }).mount('#app');

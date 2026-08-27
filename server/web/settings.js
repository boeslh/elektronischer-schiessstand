// Einstellungen – in localStorage gespeichert, von allen Seiten lesbar.
const SETTINGS_KEY = 'schiessstand_settings';
const SETTINGS_DEFAULTS = { pageSize: 50, laneFrom: 1, laneTo: 10 };

function getSettings() {
  try {
    const raw = localStorage.getItem(SETTINGS_KEY);
    return raw ? Object.assign({}, SETTINGS_DEFAULTS, JSON.parse(raw)) : { ...SETTINGS_DEFAULTS };
  } catch { return { ...SETTINGS_DEFAULTS }; }
}

function saveSettings(patch) {
  const cur = getSettings();
  localStorage.setItem(SETTINGS_KEY, JSON.stringify(Object.assign(cur, patch)));
}

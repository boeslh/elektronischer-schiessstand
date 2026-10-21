package main

import "testing"

// Jeder Schluessel der Admin-Konfigurationstabelle muss aus dem SHOW-
// Telegramm lesbar sein - sonst fehlt er in der Sicherung und koennte nach
// einem Platinentausch nicht wiederhergestellt werden.
func TestDeviceConfigSettings_CoversAllAdminKeys(t *testing.T) {
	c := transferTestConfig415()
	c.PaperTrigger = "piezo"
	got := deviceConfigSettings(c)
	for _, k := range adminConfigKeys {
		if k.Key == "ZHOLE" {
			continue // setzt der Stand-PC je Scheibe selbst, bewusst nicht gesichert
		}
		if _, ok := got[k.Key]; !ok {
			t.Errorf("Schluessel %s wird nicht gesichert (fehlt in currentSettingValue)", k.Key)
		}
	}
	if _, ok := got["ZHOLE"]; ok {
		t.Error("ZHOLE darf nicht gesichert werden (wechselt bei LG/LP)")
	}
	if got["PAPERTRIGGER"] != "PIEZO" || got["ALGO"] != "CLASSIC" || got["MAXSIGMA"] != "2.00" {
		t.Errorf("Formatierung: PAPERTRIGGER=%s ALGO=%s MAXSIGMA=%s", got["PAPERTRIGGER"], got["ALGO"], got["MAXSIGMA"])
	}
}

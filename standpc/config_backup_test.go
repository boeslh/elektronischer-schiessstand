package main

import "testing"

// Jeder Schluessel der Admin-Konfigurationstabelle muss aus dem SHOW-
// Telegramm lesbar sein - sonst fehlt er in der Sicherung und koennte nach
// einem Platinentausch nicht wiederhergestellt werden.
func TestDeviceConfigSettings_CoversAllAdminKeys(t *testing.T) {
	for _, fw := range []string{"4.15", "4.20"} {
		c := transferTestConfig415()
		other := adminKeysFrom420 // Schluessel, die dieses Geraet nicht kennt
		if fw == "4.20" {
			c.StandoffPaperMm, c.MicHalfXMm, c.SoundMps, c.MicOffsetNs, c.SoundSetMps = nil, nil, nil, nil, nil
			temp := 20.0
			c.MicLayout, c.MicDistUm, c.TempSetC = "angled", &[6]int{}, &temp
			other = adminKeysUntil419
		}
		got := deviceConfigSettings(c)
		for _, k := range adminConfigKeys {
			if k.Key == "ZHOLE" {
				continue // setzt der Stand-PC je Scheibe selbst, bewusst nicht gesichert
			}
			if _, ok := got[k.Key]; ok == other[k.Key] {
				t.Errorf("Firmware %s: Schluessel %s gesichert=%v, erwartet %v", fw, k.Key, ok, !other[k.Key])
			}
		}
		if want := map[string]string{"4.15": "flat", "4.20": "angled"}[fw]; got["MICLAYOUT"] != want {
			t.Errorf("Firmware %s: MICLAYOUT=%q, erwartet %q", fw, got["MICLAYOUT"], want)
		}
	}
	c := transferTestConfig415()
	c.PaperTrigger = "piezo"
	got := deviceConfigSettings(c)
	if _, ok := got["ZHOLE"]; ok {
		t.Error("ZHOLE darf nicht gesichert werden (wechselt bei LG/LP)")
	}
	if got["PAPERTRIGGER"] != "PIEZO" || got["ALGO"] != "CLASSIC" || got["MAXSIGMA"] != "2.00" {
		t.Errorf("Formatierung: PAPERTRIGGER=%s ALGO=%s MAXSIGMA=%s", got["PAPERTRIGGER"], got["ALGO"], got["MAXSIGMA"])
	}
}

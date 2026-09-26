package main

import (
	"reflect"
	"testing"
)

func TestDiffConfig(t *testing.T) {
	prev := map[string]string{"OFS1": "812", "OFFSETX": "0", "ALGO": "CLASSIC"}
	next := map[string]string{"OFS1": "834", "OFFSETX": "0", "ALGO": "CLASSIC", "PIEZO": "1"}
	ch, old := diffConfig(prev, next)
	if !reflect.DeepEqual(ch, map[string]string{"OFS1": "834", "PIEZO": "1"}) {
		t.Errorf("changes = %v", ch)
	}
	if !reflect.DeepEqual(old, map[string]string{"OFS1": "812"}) {
		t.Errorf("previous = %v (neuer Schluessel PIEZO darf keinen Vorwert haben)", old)
	}
	if ch, _ := diffConfig(next, next); len(ch) != 0 {
		t.Errorf("unveraenderte Konfiguration ergibt Aenderungen %v", ch)
	}
}

func TestClassifyConfigChange(t *testing.T) {
	for _, c := range []struct {
		first, mac bool
		changes    map[string]string
		want       string
	}{
		{true, false, map[string]string{"OFS1": "1"}, "erstmalig"},
		{false, true, map[string]string{}, "platinentausch"},
		{false, false, map[string]string{"SOUNDSPEED": "340"}, "einstellungen"}, // SOUNDSPEED ist manuell, nicht CAL START
		{false, false, map[string]string{"OFS3": "5", "DEBUG": "0"}, "kalibrierung"},
		{false, false, map[string]string{"OFFSETX": "1500"}, "einstellungen"},
	} {
		if got := classifyConfigChange(c.first, c.mac, c.changes); got != c.want {
			t.Errorf("%+v -> %s, want %s", c, got, c.want)
		}
	}
}

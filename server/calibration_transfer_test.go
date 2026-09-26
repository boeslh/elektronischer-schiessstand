package main

import (
	"strings"
	"testing"
	"time"
)

// Gleiche Erwartung wie standpc/calibration_transfer_test.go
// unchangedSettings() - beide Seiten muessen dieselben Schluessel und
// Formate verwenden, sonst erkennt der Stand-PC Unterschiede falsch.
func TestFirmwareSettings_MatchesStandPC(t *testing.T) {
	p := DefaultSimParams()
	p.MicOffsetNs = [6]int64{0, 100, 200, 300, 400, 500}
	p.ClusterRadiusUm = 0 // alte Config ohne RADIUS -> Firmware-Default 200
	p.MinMics = 0         // -> Fallback 5
	p.BulletShiftPct = 50
	var got []string
	for _, s := range firmwareSettings(p) {
		got = append(got, s.Key+"="+s.Value)
	}
	want := "STANDOFFPAPER=28.00 MICHALFX=115.00 OFFSETX=0 OFFSETY=0 SOUNDSPEED=343 " +
		"OFS0=0 OFS1=100 OFS2=200 OFS3=300 OFS4=400 OFS5=500 BSHIFTPCT=50 BSHIFTCAP=3.00 RADIUS=200 " +
		"MICEN0=1 MICEN1=1 MICEN2=1 MICEN3=1 MICEN4=1 MICEN5=1 ALGO=CLASSIC PELLETR=2.25 MAXSIGMA=2.00 " +
		"PIEZOMIN=100 PIEZOMAX=1400 MINMICS=5 PIEZO=1"
	if strings.Join(got, " ") != want {
		t.Errorf("firmwareSettings\n  %s\nerwartet\n  %s", strings.Join(got, " "), want)
	}
}

func TestTransferConfigName(t *testing.T) {
	ts := time.Date(2026, 9, 26, 18, 5, 9, 0, time.Local)
	if got, want := transferConfigName(3, "", ts), "Freigabe Stand 3 26.09.2026 18:05:09"; got != want {
		t.Errorf("ohne Notiz: %q, erwartet %q", got, want)
	}
	if got, want := transferConfigName(3, "Foto", ts), "Freigabe Stand 3 26.09.2026 18:05:09 – Foto"; got != want {
		t.Errorf("mit Notiz: %q, erwartet %q", got, want)
	}
}

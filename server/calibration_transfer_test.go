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
	p.MicDistUm = [6]int64{0, 100, 200, 300, 400, 500}
	p.ClusterRadiusUm = 0 // alte Config ohne RADIUS -> Firmware-Default 200
	p.BulletShiftPct = 50
	var got []string
	for _, s := range firmwareSettings(p) {
		got = append(got, s.Key+"="+s.Value)
	}
	want := "MICLAYOUT=angled OFFSETX=0 OFFSETY=0 TEMP=20.0 " +
		"DIST0=0 DIST1=100 DIST2=200 DIST3=300 DIST4=400 DIST5=500 BSHIFTPCT=50 BSHIFTCAP=3.00 RADIUS=200 " +
		"CLUSTERCORR=1 CLUSTERITER=5 LSQ=0 LSQPELLETR=2.25 " +
		"MICEN0=1 MICEN1=1 MICEN2=1 MICEN3=1 MICEN4=1 MICEN5=1 ALGO=CLASSIC " +
		"PIEZOMIN=100 PIEZOMAX=1400 PIEZO=1 ZONES=0"
	if strings.Join(got, " ") != want {
		t.Errorf("firmwareSettings\n  %s\nerwartet\n  %s", strings.Join(got, " "), want)
	}
}

// Zonen-Saetze: Format wie standpc ZoneSetValue.zcalValue, ZONES zuletzt.
func TestFirmwareSettings_Zones(t *testing.T) {
	p := DefaultSimParams()
	p.Zones = &ZoneParams{Enabled: true, HoleRadiusMM: 15.25,
		A: &ZoneSet{MicDistUm: [6]int64{0, 470, -3650, -1400, -2170, -220}, OffsetXUm: -845, OffsetYUm: -2577}}
	var got []string
	for _, s := range zoneFirmwareSettings(p) {
		got = append(got, s.Key+"="+s.Value)
	}
	want := "ZBAND=45000 ZBLEND=2250 ZCALA=0,470,-3650,-1400,-2170,-220,-845,-2577 ZCALB=OFF ZCALC=OFF ZONES=1"
	if strings.Join(got, " ") != want {
		t.Errorf("zoneFirmwareSettings\n  %s\nerwartet\n  %s", strings.Join(got, " "), want)
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

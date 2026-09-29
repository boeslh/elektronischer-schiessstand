package main

import (
	"strings"
	"testing"
	"time"
)

func transferTestConfig() *ConfigTelegram {
	one, five, zero, pr := 1, 5, 0, 2.25
	return &ConfigTelegram{
		ClusterCorr: &one, ClusterIter: &five, LSQ: &zero, LSQPelletRMm: &pr,
		StandoffPaperMm: 28, MicHalfXMm: 115, OffsetXUm: 0, OffsetYUm: 0, SoundMps: 343,
		MicOffsetNs: [6]int{0, 100, 200, 300, 400, 500}, MicEnabled: [6]int{1, 1, 1, 1, 1, 1},
		BulletShiftPct: 50, BulletShiftCapMm: 3, ClusterRadiusUm: 200, Algo: "classic",
		PelletRMm: 2.25, MaxSigmaUm: 2000, PiezoMinUs: 100, PiezoMaxUs: 1400, MinMics: 5, UsePiezo: 1,
	}
}

// Dieselbe Schluesselliste/Formatierung wie server/calibration_transfer.go
// firmwareSettings() fuer DefaultSimParams mit den obigen Offsets.
func unchangedSettings() []firmwareSetting {
	kv := "STANDOFFPAPER=28.00 MICHALFX=115.00 OFFSETX=0 OFFSETY=0 SOUNDSPEED=343 " +
		"OFS0=0 OFS1=100 OFS2=200 OFS3=300 OFS4=400 OFS5=500 BSHIFTPCT=50 BSHIFTCAP=3.00 RADIUS=200 " +
		"CLUSTERCORR=1 CLUSTERITER=5 LSQ=0 LSQPELLETR=2.25 " +
		"MICEN0=1 MICEN1=1 MICEN2=1 MICEN3=1 MICEN4=1 MICEN5=1 ALGO=CLASSIC " +
		"PIEZOMIN=100 PIEZOMAX=1400 PIEZO=1"
	var out []firmwareSetting
	for _, f := range strings.Fields(kv) {
		k, v, _ := strings.Cut(f, "=")
		out = append(out, firmwareSetting{k, v})
	}
	return out
}

func TestDiffTransfer_AllKeysKnownAndUnchanged(t *testing.T) {
	items := diffTransfer(transferTestConfig(), unchangedSettings())
	for _, it := range items {
		if _, ok := currentSettingValue(transferTestConfig(), it.Key); !ok {
			t.Errorf("Schluessel %s im SHOW-Telegramm nicht abgebildet", it.Key)
		}
		if it.Changed {
			t.Errorf("%s: aktuell %q, Ziel %q - faelschlich als geaendert erkannt", it.Key, it.Current, it.Target)
		}
	}
	if cmds := transferCommands(items); len(cmds) != 0 {
		t.Errorf("keine Befehle erwartet, bekommen %v", cmds)
	}
}

func TestTransferCommands(t *testing.T) {
	s := unchangedSettings()
	set := func(k, v string) {
		for i := range s {
			if s[i].Key == k {
				s[i].Value = v
			}
		}
	}
	set("OFS3", "-1234")
	set("OFFSETX", "1500")
	set("RADIUS", "4000")
	set("STANDOFFPAPER", "28.004") // innerhalb der Toleranz -> unveraendert
	cmds := transferCommands(diffTransfer(transferTestConfig(), s))
	want := []string{
		"CAL IMPORT SOUNDSPEED=343,OFS0=0,OFS1=100,OFS2=200,OFS3=-1234,OFS4=400,OFS5=500",
		"SET OFFSETX=1500",
		"SET RADIUS=4000",
	}
	if strings.Join(cmds, "|") != strings.Join(want, "|") {
		t.Errorf("Befehle\n  %v\nerwartet\n  %v", cmds, want)
	}
}

// Die laengstmoegliche CAL IMPORT-Zeile muss inkl. Korrelations-ID und
// TCP-Signatur in den Firmware-Puffer (AUTH_CMD_BUF_LEN 220) passen.
func TestTransferCommands_CalImportFitsFirmwareBuffer(t *testing.T) {
	s := unchangedSettings()
	for i := range s {
		if strings.HasPrefix(s[i].Key, "OFS") {
			s[i].Value = "-20000"
		}
	}
	cmds := transferCommands(diffTransfer(transferTestConfig(), s))
	line := cmds[0] + " #999999"
	if n := len(line) + 65; n > 220 { // 64 Hex-Zeichen Signatur + Trennzeichen
		t.Errorf("CAL IMPORT-Zeile %d Zeichen inkl. Signatur - Firmware-Puffer 220", n)
	}
}

func TestDiffTransfer_NoDevice(t *testing.T) {
	for _, it := range diffTransfer(nil, unchangedSettings()) {
		if !it.Changed || it.Current != "" {
			t.Fatalf("ohne Geraetekonfiguration muss alles als offen gelten: %+v", it)
		}
	}
}

// fakeESP32 beantwortet jeden per DeviceLink gesendeten Befehl wie die
// Firmware bis Rev 4.11.5: SHOW liefert das lange config-Telegramm OHNE
// "corr" (passt nicht in den corr-Hilfspuffer), kurze Antworten mit corr.
type fakeESP32 struct {
	cmds  *CommandManager
	state *DeviceState
}

func (f *fakeESP32) Write(p []byte) (int, error) {
	line := strings.TrimSpace(string(p))
	cmd, id, _ := strings.Cut(line, " #")
	go func() {
		switch cmd {
		case "SHOW":
			dispatchLine([]byte(`{"type":"config","mac":"AA:BB","lane":1,"debounce_ms":300}`), nil, f.cmds, f.state)
		default:
			dispatchLine([]byte(`{"type":"ok","corr":`+id+`}`), nil, f.cmds, f.state)
		}
	}()
	return len(p), nil
}

func TestQueryTelegram_ConfigWithoutCorr(t *testing.T) {
	link := NewDeviceLink(nil)
	ws := &WebServer{cmds: NewCommandManager(link), deviceState: NewDeviceState()}
	link.SetActive(&fakeESP32{cmds: ws.cmds, state: ws.deviceState}, true)

	start := time.Now()
	var cfg ConfigTelegram
	if err := ws.queryTelegram("SHOW", "config", &cfg); err != nil {
		t.Fatalf("SHOW ohne corr: %v", err)
	}
	if cfg.MAC != "AA:BB" {
		t.Fatalf("falsches config-Telegramm: %+v", cfg)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("Antwort erst nach %s - sollte nicht auf den Timeout warten", d)
	}
}

// Geraet mit Firmware vor Rev 4.12.0 kennt CLUSTERCORR/CLUSTERITER/LSQ/
// LSQPELLETR nicht (fehlen im SHOW) - die Uebernahme ueberspringt sie, statt
// SET-Befehle zu senden, die die Firmware mit Fehler ablehnen wuerde.
func TestDiffTransfer_OldFirmwareSkipsNewKeys(t *testing.T) {
	c := transferTestConfig()
	c.ClusterCorr, c.ClusterIter, c.LSQ, c.LSQPelletRMm = nil, nil, nil, nil
	s := unchangedSettings()
	for i := range s {
		if s[i].Key == "CLUSTERITER" {
			s[i].Value = "3"
		}
	}
	items := diffTransfer(c, s)
	for _, it := range items {
		if firmwareOptionalKeys[it.Key] && (!it.Unsupported || it.Changed) {
			t.Errorf("%s: unsupported=%v changed=%v, erwartet uebersprungen", it.Key, it.Unsupported, it.Changed)
		}
	}
	if cmds := transferCommands(items); len(cmds) != 0 {
		t.Errorf("keine Befehle erwartet, bekommen %v", cmds)
	}
}

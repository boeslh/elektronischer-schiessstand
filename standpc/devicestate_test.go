package main

import "testing"

// Firmware 4.15: status/config-Felder werden gelesen und einem nachfolgenden
// Schuss als Kontext mitgegeben (Schallgeschwindigkeit, Erfassung, WALKK),
// air_w_ns inkl. null-Eintraegen wird uebernommen.
func TestDispatch_Firmware415ShotContext(t *testing.T) {
	st := NewDeviceState()
	out := make(chan RawShot, 1)
	dispatchLine([]byte(`{"type":"status","version":"4.15.0","mac":"AA","lane":1,"capture":"mcpwm",`+
		`"cap_sync_ns":-120,"cap_check_ns":85,"cap_reanchor":0,"sound_act_mps":343.85,"sound_src":"dht",`+
		`"temp_c":20.0,"rh_pct":50.0,"dht_age_s":12,"dht_err":0}`), out, nil, st)
	dispatchLine([]byte(`{"type":"config","mac":"AA","capture":"mcpwm","walk_k":0.420,"walk_cal":0,`+
		`"walk_max_us":150,"sound_mps":343,"sound_set_mps":342.5,"dht":1}`), out, nil, st)
	dispatchLine([]byte(`{"type":"shot","seq":7,"algo":"classic","air_ns":[[0],[820],[],[930],[1200],[1300]],`+
		`"air_w_ns":[41000,38000,null,40000,null,39500],"x_um":100,"y_um":-200,"pos_valid":1,"clean":1,"hits":5,"ts":1}`),
		out, nil, st)
	raw := <-out
	if len(raw.AirWNs) != 6 || raw.AirWNs[2] != nil || raw.AirWNs[0] == nil || *raw.AirWNs[0] != 41000 {
		t.Fatalf("air_w_ns: %+v", raw.AirWNs)
	}
	if raw.Algo != "classic" || raw.Ctx.Capture != "mcpwm" || raw.Ctx.SoundActMps == nil || *raw.Ctx.SoundActMps != 343.85 ||
		raw.Ctx.WalkK == nil || *raw.Ctx.WalkK != 0.42 {
		t.Fatalf("Kontext: algo=%q ctx=%+v", raw.Algo, raw.Ctx)
	}
	if !st.DHTActive() {
		t.Error("DHT=1 nicht erkannt")
	}
	shot := processShot(raw, testScorer(t))
	if shot.Capture != "mcpwm" || shot.WalkK == nil || len(shot.AirWNs) != 6 {
		t.Errorf("processShot: %+v", shot)
	}
	s := st.Snapshot()
	if s.Status.TempC == nil || *s.Status.TempC != 20 || s.Config.SoundSetMps == nil || *s.Config.SoundSetMps != 342.5 {
		t.Errorf("Snapshot: status=%+v config=%+v", s.Status, s.Config)
	}
}

func TestCaptureMismatch(t *testing.T) {
	isr, mc := "isr", "mcpwm"
	cases := []struct {
		cal    *string
		dev    string
		differ bool
	}{
		{nil, "", false}, {nil, "isr", false}, {nil, "mcpwm", true}, // alte Sicherung = ISR
		{&isr, "isr", false}, {&mc, "mcpwm", false}, {&mc, "isr", true}, {&mc, "", true},
	}
	for _, c := range cases {
		if got := captureMismatch(c.cal, c.dev) != ""; got != c.differ {
			t.Errorf("captureMismatch(%v,%q) = %v, erwartet %v", c.cal, c.dev, got, c.differ)
		}
	}
	if cmd := calImportCommand([6]int{0, 1, 2, 3, 4, 5}, 342.5, &ConfigTelegram{SoundSetMps: new(float64)}); cmd !=
		"CAL IMPORT OFS0=0,OFS1=1,OFS2=2,OFS3=3,OFS4=4,OFS5=5,SOUNDSPEED=342.5" {
		t.Errorf("4.15: %s", cmd)
	}
	if cmd := calImportCommand([6]int{}, 342.5, &ConfigTelegram{}); cmd[len(cmd)-14:] != "SOUNDSPEED=343" {
		t.Errorf("alt: %s", cmd)
	}
}

// Abgeschnittenes config-Telegramm (Firmware 4.16.0, ~900 Zeichen): die
// vollstaendigen Felder werden uebernommen, die Sicherung unterbleibt.
func TestDispatch_TruncatedConfig(t *testing.T) {
	st := NewDeviceState()
	saved := 0
	st.SetConfigHandler(func(ConfigTelegram) { saved++ })
	line := `{"type":"config","mac":"AA","lane":1,"mic_offset_ns":[0,2929,-3659,-1120,-9921,-2000],"capture":"mcpwm",` +
		`"walk_k":0.500,"sound_set_mps":343.0,"raw_log":1,"paper_feed_mm":15.00,"paper_trigger":"piezo","paper_d`
	dispatchLine([]byte(line), make(chan RawShot, 1), nil, st)
	c := st.Snapshot().Config
	if c == nil || !c.Truncated || c.Capture != "mcpwm" || c.MicOffsetNs[2] != -3659 || c.PaperTrigger != "piezo" {
		t.Fatalf("config: %+v", c)
	}
	if saved != 0 {
		t.Error("abgeschnittene Konfiguration darf nicht gesichert werden")
	}
	if salvageJSON([]byte(`{"a":1,"b":"x`)) == nil || string(salvageJSON([]byte(`{"a":1,"b":"x`))) != `{"a":1}` {
		t.Error("salvageJSON")
	}
}

func TestZoneHoleUm(t *testing.T) {
	for no, want := range map[string]int{"1": 15250, "7": 29750} {
		if got, ok := zoneHoleUm(builtinTargets[no]); !ok || got != want {
			t.Errorf("Scheibe %s: %d/%v, erwartet %d", no, got, ok, want)
		}
	}
	if _, ok := zoneHoleUm(builtinTargets["2"]); ok {
		t.Error("ZS-Scheibe darf keinen Lochradius liefern")
	}
}

func TestZoneTransferCommands(t *testing.T) {
	intp := func(v int) *int { return &v }
	c := &ConfigTelegram{Zones: intp(1), ZHoleUm: intp(15250), ZoneSets: &ZonesTelegram{ZBandUm: 45000, ZBlendUm: 2250,
		A: &ZoneSetValue{SoundMps: 336.9, MicOffsetNs: [6]int{0, 1, 2, 3, 4, 5}, OffsetXUm: -845, OffsetYUm: 7}}}
	set := []firmwareSetting{{"ZCALA", "336.9,0,1,2,3,4,5,-845,7"}, {"ZCALB", "340.0,0,0,0,0,0,0,10,20"}, {"ZCALC", "OFF"}, {"ZBAND", "45000"}, {"ZONES", "1"}}
	cmds := transferCommands(diffTransfer(c, set))
	if len(cmds) != 1 || cmds[0] != "SET ZCAL=B,340.0,0,0,0,0,0,0,10,20" {
		t.Fatalf("Befehle %v", cmds)
	}
	// Firmware ohne Zonen: alles uebersprungen
	if cmds := transferCommands(diffTransfer(&ConfigTelegram{}, set)); len(cmds) != 0 {
		t.Fatalf("alte Firmware: %v", cmds)
	}
}

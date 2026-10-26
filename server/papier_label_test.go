package main

import (
	"strings"
	"testing"
)

func TestDPLLabel(t *testing.T) {
	out := string(dplLabel(paperLabel{Serial: "1042", ScheibeName: "Glücksscheibe Papier", TeilnehmerNr: 17, Name: "Jürgen Weiß"}))
	for _, want := range []string{"\x02L\r", "m\r", "1e22120015", "1042\r", "Gluecksscheibe Papier 1042\r", "17 Juergen Weiss\r", "Q0001\rE\r"} {
		if !strings.Contains(out, want) {
			t.Errorf("fehlt %q in %q", want, out)
		}
	}
	long := string(dplLabel(paperLabel{Serial: "12345678901234", ScheibeName: strings.Repeat("Sehr lange Scheibe ", 4), TeilnehmerNr: 123, Name: strings.Repeat("Name ", 10)}))
	if !strings.Contains(long, "1e11") {
		t.Errorf("lange Nummer sollte schmale Striche nutzen: %q", long)
	}
	for _, line := range strings.Split(long, "\r") {
		if strings.HasPrefix(line, "1212000") && len(line)-15 > labelMaxChars {
			t.Errorf("Zeile zu lang: %q", line)
		}
	}
}

func TestDPLJobPositioning(t *testing.T) {
	label := []byte("LABEL")
	// ohne Einstellungen: nur Masseinheit, Drucker-Einstellungen bleiben
	if got := string(dplJob(PapierSettings{}, label)); got != "\x02m\x02c0000LABEL" {
		t.Errorf("ohne Positionierung: %q", got)
	}
	present := 12.0
	got := string(dplJob(PapierSettings{LabelPresentMM: &present, LabelSOPAdjustMM: -1.5}, label))
	want := "\x02m\x02c0000\x02O0544\x02Kf0120\r\x02f664LABEL"
	if got != want {
		t.Errorf("mit Positionierung:\n got %q\nwant %q", got, want)
	}
	zero := 0.0
	if got := string(dplJob(PapierSettings{LabelPresentMM: &zero}, label)); got != "\x02m\x02c0000\x02Kf0000\rLABEL" {
		t.Errorf("Vorschub 0: %q", got)
	}
	pitch := 32.0
	if got := string(dplJob(PapierSettings{LabelPitchMM: &pitch}, label)); got != "\x02m\x02c0320LABEL" {
		t.Errorf("fester Abstand: %q", got)
	}
}

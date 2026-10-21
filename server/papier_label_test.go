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

package main

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// fakePaperESP32 beantwortet ACTION TARGETCHANGE; die ersten busy Anfragen
// mit "paper feed busy" (Vorschub des letzten Schusses laeuft noch).
type fakePaperESP32 struct {
	cmds  *CommandManager
	state *DeviceState
	mu    sync.Mutex
	busy  int
	sent  int // erfolgreich ausgeloeste Vorschuebe
}

func (f *fakePaperESP32) Write(p []byte) (int, error) {
	cmd, id, _ := strings.Cut(strings.TrimSpace(string(p)), " #")
	go func() {
		f.mu.Lock()
		reply := `{"type":"ok","action":"targetchange","corr":` + id + `}`
		if cmd == "ACTION TARGETCHANGE" {
			if f.busy > 0 {
				f.busy--
				reply = `{"type":"error","msg":"paper feed busy","corr":` + id + `}`
			} else {
				f.sent++
			}
		}
		f.mu.Unlock()
		dispatchLine([]byte(reply), nil, f.cmds, f.state)
	}()
	return len(p), nil
}

func (f *fakePaperESP32) count() int { f.mu.Lock(); defer f.mu.Unlock(); return f.sent }

func waitTC(t *testing.T, ws *WebServer) {
	t.Helper()
	for i := 0; i < 200; i++ {
		ws.targetChange.mu.Lock()
		r := ws.targetChange.running
		ws.targetChange.mu.Unlock()
		if !r {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Vorschub-Goroutine endet nicht")
}

func TestTargetChange_OncePerShot(t *testing.T) {
	link := NewDeviceLink(nil)
	ws := &WebServer{cmds: NewCommandManager(link), deviceState: NewDeviceState()}
	dev := &fakePaperESP32{cmds: ws.cmds, state: ws.deviceState, busy: 2}
	link.SetActive(dev, true)

	// neue Scheibe, dann sofort Probe -> Wertung ohne Schuss: nur EIN Vorschub
	ws.RequestTargetChange("neue Scheibe")
	ws.RequestTargetChange("Probe -> Wertung")
	waitTC(t, ws)
	ws.RequestTargetChange("Probe -> Wertung")
	waitTC(t, ws)
	if n := dev.count(); n != 1 {
		t.Fatalf("%d Vorschübe, erwartet 1", n)
	}
	// erster Schuss danach traegt den Leervorschub, der zweite nicht
	if n := ws.ConsumeTargetChange(); n != 1 {
		t.Fatalf("erster Schuss: paper_feeds_before=%d, erwartet 1", n)
	}
	if n := ws.ConsumeTargetChange(); n != 0 {
		t.Fatalf("zweiter Schuss: paper_feeds_before=%d, erwartet 0", n)
	}
	// nach einem Schuss loest der naechste Wechsel wieder aus
	ws.RequestTargetChange("Probe -> Wertung")
	waitTC(t, ws)
	if n := dev.count(); n != 2 {
		t.Fatalf("%d Vorschübe, erwartet 2", n)
	}
}

func TestTargetChange_PaperAutoOff(t *testing.T) {
	link := NewDeviceLink(nil)
	ws := &WebServer{cmds: NewCommandManager(link), deviceState: NewDeviceState()}
	dev := &fakePaperESP32{cmds: ws.cmds, state: ws.deviceState}
	link.SetActive(dev, true)
	ws.deviceState.SetConfig(ConfigTelegram{PaperAuto: 0, PaperFeedMm: 15})
	ws.RequestTargetChange("neue Scheibe")
	waitTC(t, ws)
	if dev.count() != 0 || ws.ConsumeTargetChange() != 0 {
		t.Fatal("bei PAPERAUTO=0 darf kein Vorschub ausgelöst werden")
	}
}

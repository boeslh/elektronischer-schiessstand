// ============================================================================
// rawpair.go – "raw"-Telegramme (SET RAWLOG=1, Firmware Rev 4.16.0, siehe
// protokoll-referenz.md 4.5c) dem zugehoerigen Schuss zuordnen.
//
// Die Firmware sendet zu jedem gueltigen shot direkt danach eine raw-Zeile
// mit derselben seq (alle Flanken + Parameter zum Schusszeitpunkt). Damit
// Schuss und Rohdaten gemeinsam ins lokale Schussprotokoll und in EINE
// Datenbankzeile gelangen, haelt diese Stufe einen shot zurueck, bis das
// passende raw da ist:
//   - shot.raw_follows=1: die Firmware sendet als unmittelbar naechste Zeile
//     das raw - gewartet wird nur auf diese eine Zeile (typisch wenige ms).
//     Kommt sie nicht (Verbindungsabbruch genau dazwischen), geht der Schuss
//     nach rawFollowsTimeout ohne Rohdaten weiter (Empfehlung der Firmware).
//   - shot.raw_follows=0: es kommt kein raw (RAWLOG=0 oder der shot wird aus
//     dem Offline-Puffer nachgeliefert) - keine Verzoegerung.
//   - Feld fehlt (Firmware vor dem Feld raw_follows): gewartet wird nur bei
//     aktivem RAWLOG laut config, hoechstens rawPairTimeout.
//
// Abgelehnte Schuesse (reject) bekommen von der Firmware nie ein raw und
// laufen ohne Verzoegerung durch. Jede andere Zeile als das erwartete raw
// gibt den zurueckgehaltenen Schuss sofort frei.
// ============================================================================
package main

import (
	"log"
	"time"
)

const (
	rawPairTimeout    = 600 * time.Millisecond  // ohne raw_follows (aeltere 4.16-Firmware)
	rawFollowsTimeout = 1000 * time.Millisecond // bei raw_follows=1 (doppelte Empfehlung aus protokoll-referenz.md 4.5c - Reserve fuer langsames WLAN)
)

// pairRawTelegrams liest in, gibt alle Telegramme in derselben Reihenfolge an
// out weiter und haengt raw-Telegramme an den vorausgehenden shot gleicher
// seq (RawShot.RawEdges). raw-Telegramme selbst werden nicht weitergegeben.
// rawLogActive wird je shot abgefragt (DeviceState.RawLogActive). out wird
// geschlossen, sobald in geschlossen ist.
func pairRawTelegrams(in <-chan RawShot, out chan<- RawShot, rawLogActive func() bool) {
	defer close(out)
	var pending *RawShot
	var pendingTimeout time.Duration
	var timer *time.Timer
	var timeout <-chan time.Time
	flush := func() {
		if pending != nil {
			out <- *pending
			pending = nil
		}
		if timer != nil {
			timer.Stop()
			timer, timeout = nil, nil
		}
	}
	for {
		select {
		case raw, ok := <-in:
			if !ok {
				flush()
				return
			}
			if raw.Type == "raw" {
				if pending != nil && pending.Seq == raw.Seq {
					pending.RawEdges = raw.RawEdges
					flush()
				} else {
					log.Printf("raw-Telegramm #%d ohne passenden Schuss verworfen", raw.Seq)
				}
				continue
			}
			flush() // ein neuer Schuss/Reject: vorherigen ohne raw weitergeben
			if wait := rawWait(raw, rawLogActive); wait > 0 {
				r := raw
				pending, pendingTimeout = &r, wait
				timer = time.NewTimer(wait)
				timeout = timer.C
				continue
			}
			out <- raw
		case <-timeout:
			if pending != nil {
				log.Printf("Schuss #%d: kein raw-Telegramm innerhalb von %s (SET RAWLOG=1) - ohne Rohdaten gespeichert",
					pending.Seq, pendingTimeout)
			}
			flush()
		}
	}
}

// rawWait: wie lange auf das raw-Telegramm zu diesem Telegramm gewartet wird
// (0 = gar nicht), siehe Dateikopf.
func rawWait(raw RawShot, rawLogActive func() bool) time.Duration {
	if raw.Type != "shot" {
		return 0
	}
	if raw.RawFollows != nil {
		if *raw.RawFollows == 1 {
			return rawFollowsTimeout
		}
		return 0
	}
	if rawLogActive != nil && rawLogActive() {
		return rawPairTimeout
	}
	return 0
}

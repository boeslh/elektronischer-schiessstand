// ============================================================================
// protocol.go – gemeinsames Interface fuer die Disag-Wertmaschinen-Protokolle
// RM III (Legacy, RTS/DSR-Handshake, 2400 Baud) und RM IV (ENQ/STX/ACK/NAK,
// 38400 Baud) - siehe Konzept .claude/plans/wise-scribbling-abelson.md.
//
// Beide Implementierungen laufen in einer eigenen Goroutine mit blockierenden
// Lesevorgaengen (Read-Timeout statt Polling-Timer wie im VB6-Referenzcode,
// der wegen des single-threaded Event-Loop-Modells auf Timer angewiesen war -
// in Go kann das direkt sequentiell/blockierend geschrieben werden).
// ============================================================================
package disag

// ShotEvent: ein vom RM gemeldeter Schuss (Rohwerte, Umrechnung Teiler+Winkel
// -> x/y passiert bewusst NICHT hier, sondern zentral im Server, siehe
// server/manual_result.go positionFromTeilerWinkel).
//
// Beide Protokolle liefern letztlich Teiler+Winkel: RM IV direkt, RM III
// liefert stattdessen kartesische X/Y-Abweichung (in 1/100 Ring, siehe
// RMIIIBA_2.pdf) - der rmiii.go-Treiber rechnet daraus selbst den Winkel
// (atan2, einheitenunabhaengig) und uebernimmt den vom Geraet separat
// gelieferten Teilerwert unveraendert, sodass hier nur noch ein gemeinsamer
// Teiler+Winkel-Pfad noetig ist.
type ShotEvent struct {
	ShotNo  int
	Ring    *int
	Decimal *float64
	Teiler  *float64 // 1/100mm, direkt der im Verein uebliche "Teiler" (RM IV: SCH=-Ergebniszeile; RM III: Teilerwert-Feld) - siehe server/manual_result.go centerDistanceHundredthMM
	Winkel  *float64 // Grad, 0=oben/90=rechts
	Flag    string   // "ok" | "review" | "invalid"
}

// StatusEvent: Verbindungs-/Ablaufstatus fuer die Bedienoberflaeche.
type StatusEvent struct {
	Connected bool
	Message   string
}

// EditShot: ein bereits erfasster Schuss, wie er fuer eine EDI-Antwort an
// die RM (rmiv.go handleWSCLine/sendEdit) zurueckgemeldet wird - siehe
// EventHandler.PendingShotsForEdit. Changed=true, wenn der Bediener diesen
// Schuss per CorrectShot ueberschrieben hat (Flag "V" statt "U").
type EditShot struct {
	Ring    *int
	Decimal *float64
	Teiler  *float64
	Changed bool
}

// EventHandler bekommt jedes Schuss-/Statusereignis waehrend einer laufenden
// Messreihe gemeldet - vom Aufrufer (wertmaschine/session.go) implementiert.
type EventHandler interface {
	OnShot(ShotEvent)
	OnStatus(StatusEvent)
	// AllShotsResolved/PendingShotsForEdit werden nur vom RM-IV-Treiber
	// gebraucht (Editier-Anfrage der RM, siehe rmiv.go handleWSCLine) - RM
	// III hat dafuer keine Entsprechung (siehe rmiii.go-Kommentar zur
	// bewusst nicht automatisierten 5-Schuss-Karte).
	AllShotsResolved() bool
	PendingShotsForEdit() []EditShot
}

// Machine: gemeinsames Interface fuer RM III (rmiii.go) und RM IV (rmiv.go).
type Machine interface {
	// Configure oeffnet den Port (falls noch nicht offen), sendet den
	// Konfigurationsstring und startet den Lesevorgang in einer eigenen
	// Goroutine - liefert Ergebnisse/Status ueber den beim Erzeugen
	// uebergebenen EventHandler, bis Abort()/Close() aufgerufen wird oder
	// das Geraet selbst das Ende der Messreihe meldet.
	Configure(configString string) error
	// Abort bricht eine laufende Messreihe ab (sendet ABR bei RM IV, schliesst
	// bei RM III schlicht den Lesevorgang - siehe Kommentar dort).
	Abort()
	// Recover versucht, eine blockierte/haengende Wertmaschine ohne Aus-/
	// Einschalten wieder ansprechbar zu machen (bei RM III mit echter
	// Hardware bestaetigt: EXIT+V, siehe rmiii.go). Der Aufrufer muss danach
	// erneut Configure() mit dem zuletzt verwendeten Einstellungsstring
	// aufrufen, um weiterzulesen.
	Recover() error
	Close() error
}

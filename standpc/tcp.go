// ============================================================================
// tcp.go – Netzwerk-Transport: TCP-Server fuer ESP32-Verbindungen
//
// Gegenstueck zur Firmware Rev 3.1: Der ESP32 verbindet sich als TCP-CLIENT
// zu diesem Listener. Richtung bewusst so gewaehlt (ESP32 -> PC), weil der
// Stand-PC eine feste Adresse hat und der ESP32 per DHCP wandern darf.
//
// Eigenschaften:
//   - Es wird immer nur EINE aktive Geraeteverbindung gehalten (ein Blech
//     pro Stand). Verbindet sich ein neues Geraet, wird die alte Verbindung
//     geschlossen (z.B. nach ESP32-Reboot, wenn der alte Socket noch
//     halboffen ist).
//   - Gleiches zeilenbasiertes JSON-Protokoll wie Serial -> identischer
//     Dispatch ueber dispatchLine() (transport.go).
//   - TCP-Keepalive erkennt tote Verbindungen (WLAN-Abriss) nach ~30s.
// ============================================================================
package main

import (
	"bufio"
	"context"
	"log"
	"net"
	"sync"
	"time"
)

func runTCPReader(ctx context.Context, cfg *Config, out chan<- RawShot) {
	lc := net.ListenConfig{}
	ln, err := lc.Listen(ctx, "tcp", cfg.TCPListen)
	if err != nil {
		log.Fatalf("FATAL TCP-Listener %s: %v", cfg.TCPListen, err)
	}
	defer ln.Close()
	log.Printf("TCP: warte auf ESP32 auf %s", cfg.TCPListen)

	go func() {
		<-ctx.Done()
		ln.Close() // entsperrt Accept()
	}()

	var (
		mu      sync.Mutex
		current net.Conn
	)

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				log.Printf("TCP Accept: %v", err)
				continue
			}
		}

		// Nur eine aktive Geraeteverbindung: alte ersetzen
		mu.Lock()
		if current != nil {
			log.Printf("TCP: neue Verbindung von %s ersetzt alte",
				conn.RemoteAddr())
			current.Close()
		}
		current = conn
		mu.Unlock()

		if tc, ok := conn.(*net.TCPConn); ok {
			tc.SetKeepAlive(true)
			tc.SetKeepAlivePeriod(10 * time.Second)
			tc.SetNoDelay(true)
		}
		log.Printf("TCP: ESP32 verbunden von %s", conn.RemoteAddr())

		go func(c net.Conn) {
			defer func() {
				c.Close()
				mu.Lock()
				if current == c {
					current = nil
				}
				mu.Unlock()
				log.Printf("TCP: Verbindung %s beendet", c.RemoteAddr())
			}()

			scanner := bufio.NewScanner(c)
			scanner.Buffer(make([]byte, 4096), 4096)
			for scanner.Scan() {
				dispatchLine(scanner.Bytes(), out)
			}
		}(conn)
	}
}

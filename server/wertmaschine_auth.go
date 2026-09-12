// ============================================================================
// wertmaschine_auth.go – HMAC-Absicherung der /api/wertmaschine/*-Endpunkte.
//
// Gleiches Grundmuster wie die ESP32<->StandPC-Authentifizierung
// (standpc/devicelink.go signLine/verifyAndStripLine): HMAC-SHA256 ueber
// die Nutzlast, auf 64 Bit/16 Hex-Zeichen gekuerzt, ein anlagenweiter
// Pre-Shared-Key - siehe Konzept .claude/plans/wise-scribbling-abelson.md
// Abschnitt 3.1. Fuer HTTP wird der Tag statt einem Zeilen-Praefix als
// Header "X-Wertmaschine-Auth" mitgegeben, ueber den rohen Request-Body
// berechnet.
// ============================================================================
package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
)

const wertmaschineAuthTagHexLen = 16

func wertmaschineAuthTag(psk, body []byte) string {
	mac := hmac.New(sha256.New, psk)
	mac.Write(body)
	sum := mac.Sum(nil)
	return hex.EncodeToString(sum[:wertmaschineAuthTagHexLen/2])
}

// requireWertmaschinePSK wrapt einen handlerFunc: lehnt die Anfrage ab, wenn
// kein PSK konfiguriert ist oder der Header "X-Wertmaschine-Auth" nicht zum
// HMAC-Tag des Request-Bodys passt. Liest den Body einmal komplett ein und
// setzt ihn danach wieder ein, damit der eigentliche Handler ihn normal per
// decodeBody/json.NewDecoder lesen kann.
func (a *APIServer) requireWertmaschinePSK(next handlerFunc) handlerFunc {
	return func(w http.ResponseWriter, r *http.Request) (any, error) {
		if len(a.wertmaschinePSK) == 0 {
			return nil, &httpError{code: 503, msg: "Wertmaschinen-PSK nicht konfiguriert"}
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, errBadRequest("Body nicht lesbar")
		}
		r.Body = io.NopCloser(bytes.NewReader(body))

		tag := r.Header.Get("X-Wertmaschine-Auth")
		want := wertmaschineAuthTag(a.wertmaschinePSK, body)
		if !hmac.Equal([]byte(want), []byte(tag)) {
			return nil, &httpError{code: 401, msg: "ungueltige oder fehlende Wertmaschinen-Signatur"}
		}
		return next(w, r)
	}
}

/*
 * shot_locator.h - Trefferlokalisierung fuer den Schiessstand (Rev 5 Entwurf)
 * ============================================================================
 *  Portabel (kein Arduino-Include), damit identisch auf dem ESP32 und auf dem
 *  PC (test/host_sim.cpp) laeuft.
 *
 *  Ersetzt solveAirPosition() (Dreier-Kombinationen + BSHIFT-Heuristik +
 *  Cluster-Mittelung) durch ein durchgaengiges statistisches Verfahren:
 *
 *   1. Zeitfenster aus dem Piezo ("Gate"): Der Einschlagzeitpunkt t0 muss in
 *      [anker - PIEZOMAX, anker - PIEZOMIN] liegen. Je Mikrofon kommen damit
 *      nur Flanken in [t0min + z_i/c, t0max + maxDist/c] in Frage.
 *      Muendungsknall, Echos lange danach und Stoerflanken fallen raus, egal
 *      in welcher Reihenfolge sie eintreffen.
 *
 *   2. Hypothesen (RANSAC/MSAC): Jede Dreier-Kombination mit je einer der
 *      ersten 2 Kandidatenflanken wird geschlossen geloest. Bewertet wird,
 *      wie viele UEBRIGE Mics (mit ihrer jeweils passendsten Flanke)
 *      innerhalb inlierTolNs dazu passen. Die Hypothese mit dem groessten
 *      Konsens gewinnt - ein einzelnes Echo kann so nicht mehr "gewinnen".
 *
 *   3. Verfeinerung: gewichtete Ausgleichsrechnung (Gauss-Newton, Huber-
 *      Gewichte) ueber ALLE Inlier-Mics gleichzeitig, Unbekannte (x, y, t0).
 *      Das ist der Maximum-Likelihood-Schaetzer - statistisch besser als
 *      jede Mittelung von Einzelloesungen.
 *
 *   4. Lochrand-Modell: Der Komparator loest beim ERSTEN Schall aus. Der
 *      kommt vom Punkt des Lochrands (Radius r = pelletRadiusMm), der dem
 *      jeweiligen Mikrofon am naechsten liegt:
 *          D_i = sqrt( (rho_i - r)^2 + z_i^2 ),  rho_i = |(x,y) - Mic_i|_xy
 *      Gefittet wird das LOCHZENTRUM (x, y) - der Rand geht als bekannte
 *      Geometrie ein, nicht als Heuristik.
 *
 *   5. Guete: 1-Sigma-Unsicherheit (sigmaX/sigmaY) aus der Kovarianzmatrix
 *      statt precision_um/cluster_hits.
 *
 *  Einheiten: Zeiten in ns (relativ zum Anker), Laengen in mm.
 * ============================================================================
 */
#pragma once
#include <math.h>
#include <stdint.h>
#include <string.h>

namespace shotloc {

constexpr int NMIC      = 6;
constexpr int MAX_EDGES = 8;    // Kandidatenflanken je Mic nach dem Gate

struct Geometry {
    float micX[NMIC], micY[NMIC];
    float micZ[NMIC];       // Abstand Mic <-> Scheibe (z) je Mic (Rev 4.20.0,
                            // vorher ein gemeinsamer standoffMm)
    float soundMmPerNs;     // z.B. 0.000355
    float pelletRadiusMm;   // 2.25 bei 4,5mm; 0 = Punktquelle
    float maxDistMm;        // max. Distanz Einschlag<->Mic (fuer das Gate)
};

struct Params {
    float inlierTolNs  = 3000.0f;  // RANSAC-Konsens (~1,1 mm Laufweg)
    float huberNs      = 800.0f;   // ab hier Gewicht ~1/|r|
    float sigmaPriorNs = 400.0f;   // a-priori Zeitrauschen (fuer sigmaX/Y)
    int   minMics      = 4;        // Mindestzahl Inlier fuer valid
    int   maxIter      = 15;
};

// Kandidatenflanken je Mic, ns relativ zum Anker (Piezo), aufsteigend.
struct Edges {
    uint8_t n[NMIC];
    float   t[NMIC][MAX_EDGES];
    bool    enabled[NMIC];         // SET MICEN
};

// Erlaubter Einschlagzeitpunkt t0 relativ zum Anker.
//   PAPER: t0 in [-PIEZOMAX, -PIEZOMIN]   (Piezo kommt nach dem Einschlag)
//   STEEL: t0 in [-PIEZOMAX, 0]           (Piezo quasi zeitgleich)
struct Gate {
    bool  active;
    float t0MinNs, t0MaxNs;
};

struct Result {
    bool    valid;
    float   xMm, yMm;              // Lochzentrum
    float   t0Ns;                  // geschaetzter Einschlagzeitpunkt
    float   sigmaXMm, sigmaYMm;    // 1-Sigma aus Kovarianz
    float   rmsNs;                 // RMS der Inlier-Residuen
    uint8_t nUsed;
    uint8_t usedMask;
    int8_t  edgeIdx[NMIC];         // verwendete Flanke je Mic, -1 = keine
    float   residNs[NMIC];         // Residuum je Mic (auch Outlier)
    bool    t0InGate;
};

// ---------------------------------------------------------------------------
// Modell: Laufweg vom naechstgelegenen Punkt des Lochrands zum Mic i
// ---------------------------------------------------------------------------
static inline float pathMm(const Geometry &g, int i, float x, float y,
                           float *dDdx, float *dDdy)
{
    const float dx = x - g.micX[i], dy = y - g.micY[i];
    float rho = sqrtf(dx*dx + dy*dy);
    if (rho < 1e-3f) rho = 1e-3f;
    float rr = rho - g.pelletRadiusMm;
    if (rr < 0.0f) rr = 0.0f;                      // physikalisch ausgeschlossen
    const float D = sqrtf(rr*rr + g.micZ[i]*g.micZ[i]);
    if (dDdx) {
        const float k = rr / (D * rho);
        *dDdx = k * dx;
        *dDdy = k * dy;
    }
    return D;
}

// ---------------------------------------------------------------------------
// Geschlossene Loesung fuer 3 Mics (Punktquelle, wie bisher solveAirPair()).
// Liefert x, y und s0 = c*t0 (mm). Nur Startwert - der Rand kommt im Fit.
// ---------------------------------------------------------------------------
static bool solveTrio(const Geometry &g, const int m[3], const float sMm[3],
                      float *outX, float *outY, float *outS0)
{
    int r = 0;
    for (int k = 1; k < 3; k++) if (sMm[k] < sMm[r]) r = k;
    const int a = (r + 1) % 3, b = (r + 2) % 3;
    const float Xr = g.micX[m[r]], Yr = g.micY[m[r]], Zr = g.micZ[m[r]];
    const float ra = sMm[a] - sMm[r], rb = sMm[b] - sMm[r];
    const float Xa = g.micX[m[a]], Ya = g.micY[m[a]], Za = g.micZ[m[a]];
    const float Xb = g.micX[m[b]], Yb = g.micY[m[b]], Zb = g.micZ[m[b]];
    // K = X^2+Y^2+Z^2: bei unterschiedlichem z je Mic bleibt das System
    // linear in x/y/d, z^2 geht nur als Konstante ein
    const float Kr = Xr*Xr + Yr*Yr + Zr*Zr;
    const float A1 = 2*(Xa-Xr), B1 = 2*(Ya-Yr), C1 = 2*ra, D1 = Xa*Xa+Ya*Ya+Za*Za-Kr-ra*ra;
    const float A2 = 2*(Xb-Xr), B2 = 2*(Yb-Yr), C2 = 2*rb, D2 = Xb*Xb+Yb*Yb+Zb*Zb-Kr-rb*rb;
    const float det = A1*B2 - A2*B1;
    if (fabsf(det) < 1e-6f) return false;
    const float x0 = (D1*B2 - D2*B1)/det, x1 = (C2*B1 - C1*B2)/det;
    const float y0 = (A1*D2 - A2*D1)/det, y1 = (A2*C1 - A1*C2)/det;
    const float px = x0 - Xr, py = y0 - Yr;
    const float z2 = Zr*Zr;
    const float qa = 1.0f - x1*x1 - y1*y1;
    const float qb = -2.0f*(px*x1 + py*y1);
    const float qc = -(px*px + py*py + z2);
    float d;
    if (fabsf(qa) < 1e-6f) {
        if (fabsf(qb) < 1e-6f) return false;
        d = -qc/qb;
    } else {
        const float disc = qb*qb - 4*qa*qc;
        if (disc < 0) return false;
        const float sq = sqrtf(disc);
        const float d1 = (-qb + sq)/(2*qa), d2 = (-qb - sq)/(2*qa);
        if (d1 > 0 && (d2 <= 0 || d1 < d2)) d = d1;
        else if (d2 > 0)                    d = d2;
        else return false;
    }
    if (d <= 0) return false;
    *outX  = x0 + x1*d;
    *outY  = y0 + y1*d;
    *outS0 = sMm[r] - d;
    return true;
}

// 3x3 symmetrisch loesen (Cholesky); inv optional (fuer Kovarianz)
static bool solve3(const float A[3][3], const float bv[3], float x[3], float inv[3][3])
{
    float L[3][3] = {{0}};
    for (int i = 0; i < 3; i++) {
        for (int j = 0; j <= i; j++) {
            float s = A[i][j];
            for (int k = 0; k < j; k++) s -= L[i][k]*L[j][k];
            if (i == j) { if (s <= 1e-12f) return false; L[i][i] = sqrtf(s); }
            else        L[i][j] = s / L[j][j];
        }
    }
    auto sub = [&](const float rhs[3], float out[3]) {
        float y[3];
        for (int i = 0; i < 3; i++) {
            float s = rhs[i];
            for (int k = 0; k < i; k++) s -= L[i][k]*y[k];
            y[i] = s / L[i][i];
        }
        for (int i = 2; i >= 0; i--) {
            float s = y[i];
            for (int k = i+1; k < 3; k++) s -= L[k][i]*out[k];
            out[i] = s / L[i][i];
        }
    };
    if (bv && x) sub(bv, x);
    if (inv) {
        for (int c = 0; c < 3; c++) {
            float e[3] = {0,0,0}; e[c] = 1; float col[3];
            sub(e, col);
            for (int r = 0; r < 3; r++) inv[r][c] = col[r];
        }
    }
    return true;
}

// Fuer eine Hypothese (x,y,s0): je Mic die passendste Flanke waehlen.
// Rueckgabe: Anzahl Inlier, cost = MSAC-Kosten (mm^2).
static int scoreHypothesis(const Geometry &g, const Edges &e, float x, float y, float s0,
                           float tolMm, int8_t pick[NMIC], float *cost)
{
    int n = 0; float c = 0;
    for (int i = 0; i < NMIC; i++) {
        pick[i] = -1;
        if (!e.enabled[i] || e.n[i] == 0) continue;
        const float D = pathMm(g, i, x, y, nullptr, nullptr);
        float best = 1e30f; int bi = -1;
        for (int k = 0; k < e.n[i]; k++) {
            const float r = fabsf(e.t[i][k]*g.soundMmPerNs - s0 - D);
            if (r < best) { best = r; bi = k; }
        }
        if (best < tolMm) { pick[i] = (int8_t)bi; n++; c += best*best; }
        else              { c += tolMm*tolMm; }
    }
    *cost = c;
    return n;
}

// ---------------------------------------------------------------------------
// Hauptfunktion
// ---------------------------------------------------------------------------
static bool locate(const Geometry &g, const Edges &in, const Gate &gate,
                   const Params &p, Result *res)
{
    memset(res, 0, sizeof(*res));
    for (int i = 0; i < NMIC; i++) res->edgeIdx[i] = -1;
    const float c = g.soundMmPerNs;

    // --- 1. Gate: nur physikalisch moegliche Flanken behalten -------------
    Edges e;
    for (int i = 0; i < NMIC; i++) {
        e.enabled[i] = in.enabled[i];
        e.n[i] = 0;
        if (!in.enabled[i]) continue;
        const float lo = gate.t0MinNs + g.micZ[i] / c - p.inlierTolNs;
        const float hi = gate.t0MaxNs + g.maxDistMm  / c + p.inlierTolNs;
        for (int k = 0; k < in.n[i] && e.n[i] < MAX_EDGES; k++) {
            const float t = in.t[i][k];
            if (gate.active && (t < lo || t > hi)) continue;
            e.t[i][e.n[i]++] = t;
        }
    }
    int micsAvail[NMIC], nAvail = 0;
    for (int i = 0; i < NMIC; i++) if (e.n[i] > 0) micsAvail[nAvail++] = i;
    if (nAvail < 3) return false;

    // --- 2. Hypothesen (MSAC) ---------------------------------------------
    const float tolMm = p.inlierTolNs * c;
    int   bestN = -1; float bestCost = 1e30f;
    float bx = 0, by = 0, bs = 0;
    int8_t bestPick[NMIC];
    for (int a = 0; a < nAvail; a++)
    for (int b = a+1; b < nAvail; b++)
    for (int d = b+1; d < nAvail; d++) {
        const int m[3] = { micsAvail[a], micsAvail[b], micsAvail[d] };
        const int na = e.n[m[0]] < 2 ? e.n[m[0]] : 2;
        const int nb = e.n[m[1]] < 2 ? e.n[m[1]] : 2;
        const int nd = e.n[m[2]] < 2 ? e.n[m[2]] : 2;
        for (int ka = 0; ka < na; ka++)
        for (int kb = 0; kb < nb; kb++)
        for (int kd = 0; kd < nd; kd++) {
            const float s[3] = { e.t[m[0]][ka]*c, e.t[m[1]][kb]*c, e.t[m[2]][kd]*c };
            float x, y, s0;
            if (!solveTrio(g, m, s, &x, &y, &s0)) continue;
            if (fabsf(x) > 400 || fabsf(y) > 400) continue;
            // Startwert ist eine Punktquelle - den (fast) gemeinsamen
            // Laufweg-Versatz durch den Lochrand in s0 nachziehen.
            s0 = 0;
            for (int q = 0; q < 3; q++) s0 += s[q] - pathMm(g, m[q], x, y, nullptr, nullptr);
            s0 /= 3.0f;
            if (gate.active) {
                const float t0 = s0 / c;
                if (t0 < gate.t0MinNs - p.inlierTolNs || t0 > gate.t0MaxNs + p.inlierTolNs) continue;
            }
            int8_t pick[NMIC]; float cost;
            const int n = scoreHypothesis(g, e, x, y, s0, tolMm, pick, &cost);
            if (n > bestN || (n == bestN && cost < bestCost)) {
                bestN = n; bestCost = cost; bx = x; by = y; bs = s0;
                memcpy(bestPick, pick, sizeof(pick));
            }
        }
    }
    if (bestN < 3) return false;

    // --- 3. Robuste Ausgleichsrechnung (Gauss-Newton + Huber) ---------------
    float x = bx, y = by, s0 = bs;
    int8_t pick[NMIC]; memcpy(pick, bestPick, sizeof(pick));
    const float kH = p.huberNs * c;
    float JtWJ[3][3];
    int nUsed = 0;
    for (int pass = 0; pass < 2; pass++) {
        for (int it = 0; it < p.maxIter; it++) {
            float A[3][3] = {{0}}, bvec[3] = {0};
            nUsed = 0;
            for (int i = 0; i < NMIC; i++) {
                if (pick[i] < 0) continue;
                float gx, gy;
                const float D = pathMm(g, i, x, y, &gx, &gy);
                const float r = e.t[i][pick[i]]*c - s0 - D;
                const float w = (fabsf(r) <= kH) ? 1.0f : kH / fabsf(r);
                const float J[3] = { gx, gy, 1.0f };   // d(Modell)/d(x,y,s0)
                for (int u = 0; u < 3; u++) {
                    bvec[u] += w * J[u] * r;
                    for (int v = 0; v < 3; v++) A[u][v] += w * J[u] * J[v];
                }
                nUsed++;
            }
            float step[3];
            if (!solve3(A, bvec, step, nullptr)) return false;
            x += step[0]; y += step[1]; s0 += step[2];
            memcpy(JtWJ, A, sizeof(A));
            if (fabsf(step[0]) < 1e-4f && fabsf(step[1]) < 1e-4f) break;
        }
        // nach dem ersten Fit einmal neu zuordnen (ggf. Mic dazu/weg)
        if (pass == 0) {
            float cost;
            scoreHypothesis(g, e, x, y, s0, tolMm, pick, &cost);
        }
    }

    // --- 4. Ergebnis + Kovarianz --------------------------------------------
    float ss = 0; int n = 0; uint8_t mask = 0;
    for (int i = 0; i < NMIC; i++) {
        res->residNs[i] = 0;
        if (e.n[i] == 0) continue;
        // Residuum zur passendsten Flanke (auch fuer Outlier, zur Diagnose)
        const float D = pathMm(g, i, x, y, nullptr, nullptr);
        float best = 1e30f; int bi = 0;
        for (int k = 0; k < e.n[i]; k++) {
            const float r = e.t[i][k]*c - s0 - D;
            if (fabsf(r) < fabsf(best)) { best = r; bi = k; }
        }
        res->residNs[i] = best / c;
        if (pick[i] >= 0) {
            res->edgeIdx[i] = (int8_t)bi;
            ss += best*best; n++; mask |= (uint8_t)(1u << i);
        }
    }
    float inv[3][3];
    if (!solve3(JtWJ, nullptr, nullptr, inv)) return false;
    const float sPrior = p.sigmaPriorNs * c;
    float s2 = sPrior*sPrior;
    if (n > 3) {
        const float est = ss / (float)(n - 3);
        if (est > s2) s2 = est;       // nie optimistischer als die Prior
    }
    res->xMm      = x;
    res->yMm      = y;
    res->t0Ns     = s0 / c;
    res->sigmaXMm = sqrtf(s2 * inv[0][0]);
    res->sigmaYMm = sqrtf(s2 * inv[1][1]);
    res->rmsNs    = (n > 0) ? sqrtf(ss / n) / c : 0;
    res->nUsed    = (uint8_t)n;
    res->usedMask = mask;
    res->t0InGate = !gate.active
                 || (res->t0Ns >= gate.t0MinNs - p.inlierTolNs
                     && res->t0Ns <= gate.t0MaxNs + p.inlierTolNs);
    res->valid    = (n >= p.minMics) && res->t0InGate;
    return true;
}

} // namespace shotloc

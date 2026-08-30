-- Vorbereitung für den gs26-Export (Preisschießen-Auswertung, siehe
-- gs26/backend/copy_*_pg.py): stabile Integer-ID je Scheiben-Einheit, da
-- gs26_Scheiben.ScheibenID ein MySQL int(11) ist und UUIDs dort nicht
-- direkt gespeichert werden können.
ALTER TABLE ps_kauf_scheiben ADD COLUMN gs26_scheiben_id SERIAL UNIQUE;

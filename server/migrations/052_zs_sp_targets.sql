BEGIN;

-- ZS 15m (Zimmerstutzen) und SP 25m (Sportpistole) ergaenzen - bisher waren
-- nur LG 10m ISSF und LP 10m ISSF als Scheibe waehlbar, obwohl der Stand-PC
-- bereits alle vier Scheibenformen (LG/ZS/SP/LP) kennt (standpc/targets.go).
-- Ringdurchmesser 1:1 aus standpc/targets.json (Scheibe "2" bzw. "4")
-- uebernommen, damit Grafik/Scoring am Stand-PC und die Referenzscheibe im
-- Server (target_geometry.go) exakt uebereinstimmen. Der Scheibenname muss
-- eines der Kuerzel LG/ZS/SP/LP als eigenstaendiges Wort enthalten, siehe
-- target_geometry.go matchTargetNoByName.

INSERT INTO targets (id, name, card_width_mm, card_height_mm, inner_ten_d_mm, caliber_mm, edge_scoring)
VALUES
  ('00000000-0000-0000-0000-00000000c010', 'ZS 15m', 155, 155, 0,  5.6, true),
  ('00000000-0000-0000-0000-00000000d010', 'SP 25m', 500, 500, 25, 5.6, true)
ON CONFLICT (id) DO NOTHING;

INSERT INTO target_rings (target_id, ring_value, diameter_mm) VALUES
  ('00000000-0000-0000-0000-00000000c010', 10,  4.5),
  ('00000000-0000-0000-0000-00000000c010',  9, 13.5),
  ('00000000-0000-0000-0000-00000000c010',  8, 22.5),
  ('00000000-0000-0000-0000-00000000c010',  7, 31.5),
  ('00000000-0000-0000-0000-00000000c010',  6, 40.5),
  ('00000000-0000-0000-0000-00000000c010',  5, 49.5),
  ('00000000-0000-0000-0000-00000000c010',  4, 58.5),
  ('00000000-0000-0000-0000-00000000c010',  3, 67.5),
  ('00000000-0000-0000-0000-00000000c010',  2, 76.5),
  ('00000000-0000-0000-0000-00000000c010',  1, 85.5),
  ('00000000-0000-0000-0000-00000000d010', 10,  50),
  ('00000000-0000-0000-0000-00000000d010',  9, 100),
  ('00000000-0000-0000-0000-00000000d010',  8, 150),
  ('00000000-0000-0000-0000-00000000d010',  7, 200),
  ('00000000-0000-0000-0000-00000000d010',  6, 250),
  ('00000000-0000-0000-0000-00000000d010',  5, 300),
  ('00000000-0000-0000-0000-00000000d010',  4, 350),
  ('00000000-0000-0000-0000-00000000d010',  3, 400),
  ('00000000-0000-0000-0000-00000000d010',  2, 450),
  ('00000000-0000-0000-0000-00000000d010',  1, 500)
ON CONFLICT (target_id, ring_value) DO NOTHING;

COMMIT;

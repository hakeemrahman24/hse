-- Hazard classes return to the classic five: Physical, Chemical, Biological, Ergonomic, Psychosocial.
-- The Safety (25) and Environmental (5) classes are removed; their categories move to Physical, where
-- they can be moved again one by one in Settings > Hazard categories. Observations keep their
-- hazard category, so only the class shown for them changes. Run by hand after 016, before deploying:
--   mysql hse_register < migrations/017_classic_five_classes.sql
-- The seed file has the same change, for a database seeded from scratch.

UPDATE ref_hazard_category SET hazard_class = 'Physical' WHERE hazard_class IN ('Safety', 'Environmental');
DELETE FROM ref_hazard_class WHERE name IN ('Safety', 'Environmental');

-- Number the five classes 1..5 in the classic order.
UPDATE ref_hazard_class SET sort_order = CASE name
    WHEN 'Physical' THEN 1 WHEN 'Chemical' THEN 2 WHEN 'Biological' THEN 3
    WHEN 'Ergonomic' THEN 4 WHEN 'Psychosocial' THEN 5 ELSE sort_order END;

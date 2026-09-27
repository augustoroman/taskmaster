-- Per-person tag colors: tags.color is the owner's; tag_shares.color is the
-- sharer's color when shared (the recipient's starting color); tag_prefs.color
-- is a recipient's own choice. '' means unset.
ALTER TABLE tag_prefs ADD COLUMN color TEXT NOT NULL DEFAULT '';
ALTER TABLE tag_shares ADD COLUMN color TEXT NOT NULL DEFAULT '';

-- Give existing tags a pastel color (the same palette as app.tagPalette).
UPDATE tags SET color = (CASE abs(random()) % 12
  WHEN 0 THEN '#f8b4c0' WHEN 1 THEN '#fbc4a4' WHEN 2 THEN '#fcd89a' WHEN 3 THEN '#f3eaa0'
  WHEN 4 THEN '#d2eca4' WHEN 5 THEN '#b5e6b9' WHEN 6 THEN '#a8e0d6' WHEN 7 THEN '#aed6f1'
  WHEN 8 THEN '#bcc6f5' WHEN 9 THEN '#d3bdf2' WHEN 10 THEN '#efb9e6' ELSE '#e3d3bd' END)
WHERE color = '';

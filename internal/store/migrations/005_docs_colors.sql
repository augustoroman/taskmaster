-- Move tags still on the original pastel palette (migration 004) to the new
-- default palette (Google Docs' "light 1" row; app.tagPalette),
-- and keep shares that started with the old color in step. Colors people
-- picked themselves (tag_prefs) are left alone.
CREATE TEMP TABLE old_palette (color TEXT);
INSERT INTO old_palette VALUES ('#f8b4c0'), ('#fbc4a4'), ('#fcd89a'), ('#f3eaa0'), ('#d2eca4'), ('#b5e6b9'),
  ('#a8e0d6'), ('#aed6f1'), ('#bcc6f5'), ('#d3bdf2'), ('#efb9e6'), ('#e3d3bd');

UPDATE tag_shares SET color = '' WHERE color IN (SELECT color FROM old_palette)
  AND color = (SELECT color FROM tags WHERE tags.id = tag_shares.tag_id);

UPDATE tags SET color = (CASE abs(random()) % 10
  WHEN 0 THEN '#cc4125' WHEN 1 THEN '#e06666' WHEN 2 THEN '#f6b26b' WHEN 3 THEN '#ffd966' WHEN 4 THEN '#93c47d'
  WHEN 5 THEN '#76a5af' WHEN 6 THEN '#6d9eeb' WHEN 7 THEN '#6fa8dc' WHEN 8 THEN '#8e7cc3' ELSE '#c27ba0' END)
WHERE color IN (SELECT color FROM old_palette);

UPDATE tag_shares SET color = (SELECT color FROM tags WHERE tags.id = tag_shares.tag_id) WHERE color = '';

DROP TABLE old_palette;

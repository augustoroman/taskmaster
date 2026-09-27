-- JSON snapshot for undoing the latest action (store.Undo); '' if none.
ALTER TABLE tasks ADD COLUMN undo TEXT NOT NULL DEFAULT '';

-- Fixed tasks: 1 keeps a missed date pending until done instead of skipping it.
ALTER TABLE tasks ADD COLUMN carry_over INTEGER NOT NULL DEFAULT 0;

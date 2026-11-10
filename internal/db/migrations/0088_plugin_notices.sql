-- Why the sandbox switched a plugin off, and which permissions an update asked for that were never granted.
ALTER TABLE plugins
	ADD COLUMN IF NOT EXISTS disabled_reason text NOT NULL DEFAULT '',
	ADD COLUMN IF NOT EXISTS disabled_at timestamptz,
	ADD COLUMN IF NOT EXISTS missing_permissions jsonb NOT NULL DEFAULT '[]';

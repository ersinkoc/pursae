-- Pursae Manager: where an installed plugin package came from. 'hub' packages were installed from
-- Pursae Manager and get its updates; 'upload' packages were uploaded (or came from WPGO_MARKET_DIR).
ALTER TABLE plugins ADD COLUMN IF NOT EXISTS source text NOT NULL DEFAULT 'upload'
	CHECK (source IN ('hub', 'upload'));

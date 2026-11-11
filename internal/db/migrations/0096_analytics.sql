-- Pursae Analytics: privacy-friendly, cookieless site statistics and real-user Core Web Vitals.
--
-- One row per page view or event. No IP address, cookie or user id is stored: visitor is a hash of
-- the IP address and the user agent with a salt that is replaced every day and never kept, so a
-- visitor can be counted within a day but never followed across days or identified.
CREATE TABLE IF NOT EXISTS analytics_events (
	id            bigserial PRIMARY KEY,
	ts            timestamptz NOT NULL DEFAULT now(),
	visitor       bigint      NOT NULL,           -- daily salted hash
	page_id       bigint      NOT NULL DEFAULT 0, -- random id the browser gave the view (engagement, vitals)
	name          text        NOT NULL DEFAULT 'pageview', -- pageview, outbound, download, form, 404
	path          text        NOT NULL,
	target        text        NOT NULL DEFAULT '', -- link or form target of an event
	referrer      text        NOT NULL DEFAULT '', -- host of the referring site ('' direct or internal)
	utm_source    text        NOT NULL DEFAULT '',
	utm_medium    text        NOT NULL DEFAULT '',
	utm_campaign  text        NOT NULL DEFAULT '',
	country       text        NOT NULL DEFAULT '', -- ISO 3166-1 alpha-2 from a proxy header, '' unknown
	device        text        NOT NULL DEFAULT '', -- desktop, tablet, mobile
	browser       text        NOT NULL DEFAULT '',
	os            text        NOT NULL DEFAULT '',
	engaged_ms    integer     NOT NULL DEFAULT 0,  -- time the page was visible
	lcp_ms        integer,                         -- Core Web Vitals of the view (null: not measured)
	inp_ms        integer,
	cls           real,
	ttfb_ms       integer,
	fcp_ms        integer
);
CREATE INDEX IF NOT EXISTS analytics_events_ts ON analytics_events (ts);
CREATE INDEX IF NOT EXISTS analytics_events_page ON analytics_events (page_id) WHERE page_id <> 0;

-- The salt of the day's visitor hashes: one row per day, created by the first view of the day on
-- any instance, deleted the next day (a hash can then no longer be recomputed).
CREATE TABLE IF NOT EXISTS analytics_salts (
	day  date PRIMARY KEY,
	salt bytea NOT NULL
);

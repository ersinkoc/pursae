-- Personal data requests (Tools › Export / Erase Personal Data) get their own table.
--
-- They were one JSON list in the wpgo_privacy_requests setting, rewritten whole on every change, so
-- two instances (or a stale settings cache) could lose a request. Each request is now one row; the
-- record itself stays the JSON the server marshals (data), with the fields it filters on as columns.
-- One open request per e-mail and action (wp_create_user_request) is a unique index.
CREATE TABLE IF NOT EXISTS privacy_requests (
	id         bigserial PRIMARY KEY,
	email      text NOT NULL,
	type       text NOT NULL,
	status     text NOT NULL,
	data       jsonb NOT NULL DEFAULT '{}',
	created_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO privacy_requests (id, email, type, status, data)
SELECT (r->>'ID')::bigint, coalesce(r->>'Email', ''), coalesce(r->>'Type', ''), coalesce(r->>'Status', ''), r
FROM settings, jsonb_array_elements(CASE WHEN jsonb_typeof(value) = 'array' THEN value ELSE '[]'::jsonb END) r
WHERE key = 'wpgo_privacy_requests' AND (r->>'ID') ~ '^[0-9]+$' AND (r->>'ID')::bigint > 0
ON CONFLICT (id) DO NOTHING;

SELECT setval(pg_get_serial_sequence('privacy_requests', 'id'), greatest((SELECT max(id) FROM privacy_requests), 1),
	(SELECT max(id) FROM privacy_requests) IS NOT NULL);

-- Older duplicates of an open request would block the index: keep the newest open one.
DELETE FROM privacy_requests a USING privacy_requests b
WHERE a.status <> 'request-completed' AND b.status <> 'request-completed' AND a.type = b.type
	AND lower(a.email) = lower(b.email) AND a.id < b.id;

CREATE UNIQUE INDEX IF NOT EXISTS privacy_requests_open ON privacy_requests (type, lower(email))
	WHERE status <> 'request-completed';

DELETE FROM settings WHERE key = 'wpgo_privacy_requests';

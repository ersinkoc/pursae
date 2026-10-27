-- GoWebEditor Pro (ported from upstream Pro 4.3.0, see NOTICE): form submissions and their
-- action log. Upstream keeps them in e_submissions, e_submissions_values and
-- e_submissions_actions_log; here the field values and the action log are jsonb columns.

CREATE TABLE IF NOT EXISTS gwe_form_submissions (
	id            bigserial PRIMARY KEY,
	post_id       bigint REFERENCES posts(id) ON DELETE SET NULL, -- document that holds the form
	element_id    text NOT NULL DEFAULT '',                       -- form widget id
	form_name     text NOT NULL DEFAULT '',
	referer       text NOT NULL DEFAULT '',
	referer_title text NOT NULL DEFAULT '',
	user_id       bigint REFERENCES users(id) ON DELETE SET NULL,
	user_ip       text NOT NULL DEFAULT '',
	user_agent    text NOT NULL DEFAULT '',
	fields        jsonb NOT NULL DEFAULT '[]',  -- [{"id","type","title","value"}] in form order
	meta          jsonb NOT NULL DEFAULT '{}',  -- date, time, page_url, ... as the form's metadata setting asks
	actions       jsonb NOT NULL DEFAULT '[]',  -- [{"name","label","status","log"}]
	status        text NOT NULL DEFAULT 'new' CHECK (status IN ('new', 'trash')),
	is_read       boolean NOT NULL DEFAULT false,
	created_at    timestamptz NOT NULL DEFAULT now(),
	updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS gwe_form_submissions_list ON gwe_form_submissions (status, created_at DESC);
CREATE INDEX IF NOT EXISTS gwe_form_submissions_form ON gwe_form_submissions (post_id, element_id);

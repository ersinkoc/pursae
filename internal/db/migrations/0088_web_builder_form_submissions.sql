-- Pursae Web Builder: stored form submissions (Settings › Web Builder lists them), so a message is
-- not lost when the mail fails. Deleting the page deletes its submissions.
CREATE TABLE IF NOT EXISTS web_builder_form_submissions (
	id         bigserial PRIMARY KEY,
	post_id    bigint  NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
	section_id text    NOT NULL DEFAULT '',
	form_title text    NOT NULL DEFAULT '',
	page_url   text    NOT NULL DEFAULT '',
	fields     jsonb   NOT NULL DEFAULT '{}',   -- name -> value, the validated fields only
	ip         text    NOT NULL DEFAULT '',
	mailed     boolean NOT NULL DEFAULT false,  -- the message was queued to the recipient
	created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS web_builder_form_submissions_created ON web_builder_form_submissions (created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS web_builder_form_submissions_post ON web_builder_form_submissions (post_id);

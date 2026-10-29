-- Gontact Form image CAPTCHA ([captchac] / [captchar]): one row per challenge, the rendered PNG
-- and an HMAC of the answer. Rows are single use and expire after an hour (event
-- gontact_captcha_cleanup); the table stays empty while no form uses the CAPTCHA.
CREATE TABLE IF NOT EXISTS gontact_captcha (
    prefix      text PRIMARY KEY,
    answer_hash text NOT NULL,
    image       bytea NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS gontact_captcha_created ON gontact_captcha (created_at);

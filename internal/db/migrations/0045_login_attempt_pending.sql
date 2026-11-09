-- Login attempts are stored before their password is checked (count-first throttle). Until the check
-- finishes such a row is "pending": it counts against parallel requests (so guesses cannot all slip past
-- the limit at once) but never towards a lockout, which only completed failures may start.
ALTER TABLE login_attempts ADD COLUMN IF NOT EXISTS pending boolean NOT NULL DEFAULT false;

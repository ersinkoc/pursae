-- Usernames and e-mail addresses are unique regardless of case, as in WordPress (MySQL compares
-- them case-insensitively). The store checks this before writing (LoginOrEmailTaken); the unique
-- indexes close the race between two registrations of "Bob" and "bob". A site that already holds
-- rows differing only by case keeps the plain lookup index for that column (a NOTICE says so):
-- the migration must not fail on existing data, and those accounts are resolved oldest-first
-- (UserByLoginOrEmail).
DO $$
BEGIN
	IF NOT EXISTS (SELECT 1 FROM users GROUP BY lower(login) HAVING count(*) > 1) THEN
		CREATE UNIQUE INDEX IF NOT EXISTS users_lower_login_key ON users (lower(login));
		DROP INDEX IF EXISTS users_lower_login;
	ELSE
		RAISE NOTICE 'users: logins differing only by case exist; lower(login) stays non-unique';
	END IF;
	IF NOT EXISTS (SELECT 1 FROM users GROUP BY lower(email) HAVING count(*) > 1) THEN
		CREATE UNIQUE INDEX IF NOT EXISTS users_lower_email_key ON users (lower(email));
		DROP INDEX IF EXISTS users_lower_email;
	ELSE
		RAISE NOTICE 'users: e-mail addresses differing only by case exist; lower(email) stays non-unique';
	END IF;
END
$$;

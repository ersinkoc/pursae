package store

// Pursae Forum rows that outlive the member who caused them. Both the personal data eraser
// (internal/server forumPrivacyEraser) and DeleteUser run these with $1 the member's user id, so a
// like a member gave keeps counting for the post and its author when the member is erased or the
// account is deleted.

// ForumAnonymizeLikesSQL keeps the member's likes as anonymous likes (user_id NULL, migration 0109):
// the post's like count and its author's points stay the same. Without it the users foreign key
// (ON DELETE CASCADE) would delete the likes with the account.
const ForumAnonymizeLikesSQL = `UPDATE forum_likes SET user_id = NULL WHERE user_id = $1`

// ForumAnonymizeActorSQL keeps the points ledger rows the member caused for other members (a like
// given, a manual adjustment made) without them. An event is recorded once per (kind, post,
// coalesce(actor_id, 0)) (index forum_points_event), so an event of a post that already has an
// anonymous row of that kind is merged into that row, its points added: the author's points stay
// the same and a second anonymized member does not violate the index. The users foreign key's
// ON DELETE SET NULL would violate it instead, which is why DeleteUser runs this first.
const ForumAnonymizeActorSQL = `WITH dup AS (
		SELECT DISTINCT ON (r.id) r.id AS rid, e.id AS eid, r.points FROM forum_points r
		JOIN forum_points e ON e.kind = r.kind AND e.post_id = r.post_id AND e.actor_id IS NULL
		WHERE r.actor_id = $1 AND r.kind <> 'manual' ORDER BY r.id, e.id
	), merged AS (
		UPDATE forum_points e SET points = e.points + d.points FROM dup d WHERE e.id = d.eid RETURNING e.id
	), dropped AS (
		DELETE FROM forum_points WHERE id IN (SELECT rid FROM dup) RETURNING id
	)
	UPDATE forum_points SET actor_id = NULL WHERE actor_id = $1 AND id NOT IN (SELECT rid FROM dup)`

package server

import (
	"context"
	"errors"
	"strings"
	"time"

	"wpgo/internal/cli"
	"wpgo/internal/store"
)

var contentCommentFields = []string{"comment_ID", "comment_post_ID", "comment_parent", "user_id", "comment_author",
	"comment_author_email", "comment_author_url", "comment_author_IP", "comment_agent", "comment_date",
	"comment_date_gmt", "comment_content", "comment_approved", "comment_status", "comment_type"}

var contentCommentListDefault = []string{"comment_ID", "comment_post_ID", "comment_date", "comment_approved", "comment_author", "comment_author_email"}

// contentCommentApproved maps a stored status to WordPress' comment_approved (1, 0, spam, trash).
func contentCommentApproved(status string) string {
	switch status {
	case "approved":
		return "1"
	case "pending":
		return "0"
	}
	return status
}

// contentCommentStatus reads a status as WordPress or WP-CLI write it (1, 0, approve, hold ...) and
// returns the stored one; "all" is kept for list filters. ok is false for an unknown value.
func contentCommentStatus(v string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "approve", "approved":
		return "approved", true
	case "0", "hold", "pending", "unapproved", "moderated":
		return "pending", true
	case "spam":
		return "spam", true
	case "trash":
		return "trash", true
	case "all", "":
		return "all", true
	}
	return "", false
}

func (s *Server) contentCommentRow(cm *store.Comment) cli.Row {
	return cli.Row{"comment_ID": cm.ID, "comment_post_ID": cm.PostID, "comment_parent": cm.ParentID, "user_id": cm.UserID,
		"comment_author": cm.AuthorName, "comment_author_email": cm.AuthorEmail, "comment_author_url": cm.AuthorURL,
		"comment_author_IP": cm.AuthorIP, "comment_agent": cm.UserAgent, "comment_date": cm.CreatedAt.In(s.loc()),
		"comment_date_gmt": cm.CreatedAt.UTC(), "comment_content": cm.Content, "comment_approved": contentCommentApproved(cm.Status),
		"comment_status": cm.Status, "comment_type": cm.Type}
}

// contentGetComment is GetComment with WP-CLI's not-found message.
func contentGetComment(ctx context.Context, st *store.Store, id int64) (*store.Comment, error) {
	cm, err := st.GetComment(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, cli.Errorf("Could not find the comment with ID %d.", id)
	}
	return cm, err
}

// contentPurgeComments drops the pages that show a post's comments (as applyCommentAction does).
func (s *Server) contentPurgeComments(postID int64) {
	s.purgeTags("post:"+itoa64(postID), "feed", "widgets")
}

func registerCommentCLI(r *cli.Registry, e *cliEnv) {
	r.Noun("comment", "Manage comments.")
	const purgeNote = `Running servers drop the cached pages of the post, the feeds and the widgets. No notification
e-mails are sent and Wasm plugin hooks do not fire.`

	r.Add(cli.Command{
		Path:  "comment list",
		Short: "List comments.",
		Long: `--status=all (the default) lists approved and pending comments, as the comments screen does.
comment_approved is WordPress' value (1, 0, spam, trash); comment_status the stored one.

Fields: ` + strings.Join(contentCommentFields, ", ") + `.`,
		Flags: []cli.Flag{
			{Name: "status", Usage: "all, approve(d), hold/pending, spam or trash.", Default: "all"},
			{Name: "post_id", Usage: "Only comments on this post."},
			{Name: "author_email", Usage: "Only comments by this e-mail address."},
			{Name: "user_id", Usage: "Only comments by this user (ID, login or e-mail)."},
			{Name: "type", Usage: "comment, pings, or a stored comment type (pingback, review ...)."},
			{Name: "search", Usage: "Search the content, author name, e-mail and IP."},
			{Name: "orderby", Usage: "Sort field.", Default: "date", Values: []string{"date", "comment_date", "author", "post"}},
			{Name: "order", Usage: "Sort direction.", Default: "DESC", Values: []string{"ASC", "DESC", "asc", "desc"}},
			{Name: "number", Usage: "Page size (0: all).", Default: "0"},
			{Name: "paged", Usage: "Page number (with --number).", Default: "1"},
		},
		Examples: []string{"comment list --status=hold", "comment list --post_id=12 --fields=comment_ID,comment_author,comment_content", "comment list --status=spam --format=ids"},
		Run: func(c *cli.Ctx) error {
			ctx := c.Context
			s, err := e.Server(ctx)
			if err != nil {
				return err
			}
			status, ok := contentCommentStatus(c.Flag("status"))
			if !ok {
				return cli.Usagef("invalid --status %q", c.Flag("status"))
			}
			q := store.CommentQuery{Status: status, Search: c.Flag("search"), Order: c.Flag("order"), AuthorEmail: c.Flag("author_email")}
			switch c.Flag("orderby") {
			case "author":
				q.OrderBy = "author"
			case "post":
				q.OrderBy = "post"
			}
			switch t := c.Flag("type"); t {
			case "", "comment", "pings":
				q.Type = t
			default:
				q.ExactType = t
			}
			if q.PostID, err = c.Int64("post_id", 0); err != nil {
				return err
			}
			if ref := c.Flag("user_id"); ref != "" {
				u, err := e.User(ctx, ref)
				if err != nil {
					return err
				}
				q.UserID = u.ID
			}
			number, err := c.Int("number", 0)
			if err != nil {
				return err
			}
			if q.Page, err = c.Int("paged", 1); err != nil {
				return err
			}
			q.PerPage = number
			if number <= 0 {
				q.PerPage, q.Page = 1<<31-1, 1
			}
			list, total, err := s.store.QueryComments(ctx, q)
			if err != nil {
				return err
			}
			if c.Format == "count" && c.Field == "" {
				c.Println(total)
				return nil
			}
			rows := make([]cli.Row, 0, len(list))
			for _, cm := range list {
				rows = append(rows, s.contentCommentRow(cm))
			}
			return c.Print(rows, cli.View{Fields: contentCommentListDefault, ID: "comment_ID"})
		},
	})

	r.Add(cli.Command{
		Path:     "comment get",
		Short:    "Get details about a comment.",
		Long:     "Fields: " + strings.Join(contentCommentFields, ", ") + ".",
		Args:     []cli.Arg{{Name: "id", Usage: "The comment ID."}},
		Examples: []string{"comment get 7", "comment get 7 --field=comment_content"},
		Run: func(c *cli.Ctx) error {
			id, err := c.ArgInt64(0)
			if err != nil {
				return err
			}
			s, err := e.Server(c.Context)
			if err != nil {
				return err
			}
			cm, err := contentGetComment(c.Context, s.store, id)
			if err != nil {
				return err
			}
			return c.PrintItem(s.contentCommentRow(cm), contentCommentFields)
		},
	})

	r.Add(cli.Command{
		Path:     "comment count",
		Short:    "Count comments per status, site-wide or for one post.",
		Long:     "Fields: approved, moderated, spam, trash, all (approved + moderated), total_comments.",
		Args:     []cli.Arg{{Name: "post-id", Usage: "Only this post's comments.", Optional: true}},
		Examples: []string{"comment count", "comment count 12 --field=approved"},
		Run: func(c *cli.Ctx) error {
			ctx := c.Context
			st, err := e.Store(ctx)
			if err != nil {
				return err
			}
			var counts map[string]int
			if c.Arg(0) != "" {
				pid, err := c.ArgInt64(0)
				if err != nil {
					return err
				}
				if _, err := contentGetPost(ctx, st, pid); err != nil {
					return err
				}
				counts = map[string]int{}
				rows, err := st.Pool.Query(ctx, `SELECT status, count(*) FROM comments WHERE post_id = $1 GROUP BY status`, pid)
				if err != nil {
					return err
				}
				for rows.Next() {
					var status string
					var n int
					if err := rows.Scan(&status, &n); err != nil {
						rows.Close()
						return err
					}
					counts[status] = n
				}
				rows.Close()
				if err := rows.Err(); err != nil {
					return err
				}
			} else if counts, err = st.CommentCounts(ctx); err != nil {
				return err
			}
			return c.PrintItem(contentCommentCountRow(counts), []string{"approved", "moderated", "spam", "trash", "all", "total_comments"})
		},
	})

	commentWriteFlags := []cli.Flag{
		{Name: "comment_content", Usage: "The comment text."},
		{Name: "comment_author", Usage: "The author's name."},
		{Name: "comment_author_email", Usage: "The author's e-mail."},
		{Name: "comment_author_url", Usage: "The author's website."},
		{Name: "comment_approved", Usage: "Status: 1/approve(d), 0/hold/pending, spam or trash."},
		{Name: "comment_date", Usage: "The date in the site time zone (YYYY-MM-DD[ HH:MM:SS]) or RFC 3339."},
	}

	r.Add(cli.Command{
		Path:  "comment create",
		Short: "Create a comment.",
		Long: `Approved unless --comment_approved says otherwise. With --user_id the author's name and e-mail
default to the user's. ` + purgeNote,
		Flags: append([]cli.Flag{
			{Name: "comment_post_ID", Usage: "The post the comment is on.", Required: true},
			{Name: "comment_parent", Usage: "The comment this one replies to."},
			{Name: "user_id", Usage: "The commenting user (ID, login or e-mail)."},
			{Name: "comment_author_IP", Usage: "The author's IP address."},
			{Name: "comment_agent", Usage: "The author's user agent."},
			{Name: "comment_type", Usage: "The comment type.", Default: "comment"},
			{Name: "porcelain", Usage: "Print only the comment ID.", Bool: true},
		}, commentWriteFlags...),
		Examples: []string{
			"comment create --comment_post_ID=12 --comment_content='Nice post' --comment_author=Ada",
			"comment create --comment_post_ID=12 --comment_content='Thanks' --user_id=admin --comment_parent=7 --porcelain",
		},
		Run: func(c *cli.Ctx) error {
			ctx := c.Context
			s, err := e.Server(ctx)
			if err != nil {
				return err
			}
			pid, err := c.Int64("comment_post_ID", 0)
			if err != nil {
				return err
			}
			p, err := contentGetPost(ctx, s.store, pid)
			if err != nil {
				return err
			}
			cm := &store.Comment{PostID: p.ID, Status: "approved", Type: c.Flag("comment_type"),
				AuthorIP: c.Flag("comment_author_IP"), UserAgent: c.Flag("comment_agent")}
			if strings.TrimSpace(c.Flag("comment_content")) == "" {
				return cli.Usagef("--comment_content is required")
			}
			if ref := c.Flag("user_id"); ref != "" {
				u, err := e.User(ctx, ref)
				if err != nil {
					return err
				}
				cm.UserID, cm.AuthorName, cm.AuthorEmail, cm.AuthorURL = u.ID, u.DisplayName, u.Email, u.URL
			}
			if c.Has("comment_parent") {
				parent, err := c.Int64("comment_parent", 0)
				if err != nil {
					return err
				}
				if parent > 0 {
					pc, err := contentGetComment(ctx, s.store, parent)
					if err != nil {
						return err
					}
					if pc.PostID != p.ID {
						return cli.Errorf("Comment %d is on post %d, not %d.", pc.ID, pc.PostID, p.ID)
					}
					cm.ParentID = parent
				}
			}
			if err := contentApplyCommentFields(c, s, cm); err != nil {
				return err
			}
			date := cm.CreatedAt
			if err := s.store.InsertComment(ctx, cm); err != nil {
				return err
			}
			if c.Has("comment_date") {
				// InsertComment stamps now(); a given date is written afterwards (UpdateComment).
				cm.CreatedAt = date
				if err := s.store.UpdateComment(ctx, cm); err != nil {
					return err
				}
			}
			s.contentPurgeComments(p.ID)
			if c.Bool("porcelain") {
				c.Println(cm.ID)
				return nil
			}
			c.Success("Created comment %d.", cm.ID)
			return nil
		},
	})

	r.Add(cli.Command{
		Path:     "comment update",
		Short:    "Update one or more comments.",
		Long:     "Changes only the fields given. " + purgeNote,
		Args:     []cli.Arg{{Name: "id", Usage: "Comment ID(s).", Repeat: true}},
		Flags:    commentWriteFlags,
		Examples: []string{"comment update 7 --comment_content='Edited'", "comment update 7 8 --comment_approved=hold"},
		Run: func(c *cli.Ctx) error {
			ids, err := c.ArgIDs(0)
			if err != nil {
				return err
			}
			if !contentHasAnyFlag(c, commentWriteFlags) {
				return cli.Usagef("need some fields to update")
			}
			s, err := e.Server(c.Context)
			if err != nil {
				return err
			}
			for _, id := range ids {
				cm, err := contentGetComment(c.Context, s.store, id)
				if err != nil {
					return err
				}
				if err := contentApplyCommentFields(c, s, cm); err != nil {
					return err
				}
				if err := s.store.UpdateComment(c.Context, cm); err != nil {
					return err
				}
				s.contentPurgeComments(cm.PostID)
				c.Success("Updated comment %d.", id)
			}
			return nil
		},
	})

	for _, a := range []struct{ verb, short, done string }{
		{"approve", "Approve comments.", "Approved"},
		{"unapprove", "Unapprove comments (hold them for moderation).", "Unapproved"},
		{"spam", "Mark comments as spam.", "Marked as spam"},
		{"unspam", "Restore comments from spam (to their previous status).", "Unspammed"},
		{"trash", "Move comments to the trash.", "Trashed"},
		{"untrash", "Restore comments from the trash (to their previous status).", "Untrashed"},
	} {
		r.Add(cli.Command{
			Path:     "comment " + a.verb,
			Short:    a.short,
			Long:     "Same as the comments screen's row action. " + purgeNote,
			Args:     []cli.Arg{{Name: "id", Usage: "Comment ID(s).", Repeat: true}},
			Examples: []string{"comment " + a.verb + " 7 8"},
			Run: func(c *cli.Ctx) error {
				return contentCommentActions(c, e, a.verb, a.done)
			},
		})
	}

	r.Add(cli.Command{
		Path:  "comment delete",
		Short: "Delete comments (to the trash, or permanently with --force).",
		Long: `Without --force comments go to the trash; comments already in the trash or spam, and every
comment with --force, are deleted permanently (asks for confirmation; pass --yes in scripts).
Replies move up to the deleted comment's parent. ` + purgeNote,
		Args:     []cli.Arg{{Name: "id", Usage: "Comment ID(s).", Repeat: true}},
		Flags:    []cli.Flag{{Name: "force", Usage: "Skip the trash.", Bool: true}},
		Examples: []string{"comment delete 7", "comment delete $(pursae comment list --status=spam --format=ids) --force --yes"},
		Run: func(c *cli.Ctx) error {
			ids, err := c.ArgIDs(0)
			if err != nil {
				return err
			}
			s, err := e.Server(c.Context)
			if err != nil {
				return err
			}
			force := c.Bool("force")
			type item struct {
				id   int64
				perm bool
			}
			var items []item
			perm, failed := 0, 0
			for _, id := range ids {
				cm, err := contentGetComment(c.Context, s.store, id)
				if err != nil {
					c.Warning("%v", err)
					failed++
					continue
				}
				p := force || cm.Status == "trash" || cm.Status == "spam"
				if p {
					perm++
				}
				items = append(items, item{id, p})
			}
			if perm > 0 {
				if err := c.Confirm("Permanently delete %d comment(s)? This cannot be undone.", perm); err != nil {
					return err
				}
			}
			for _, it := range items {
				action, msg := "trash", "Trashed"
				if it.perm {
					action, msg = "delete", "Deleted"
				}
				if err := s.applyCommentAction(c.Context, it.id, action); err != nil {
					c.Warning("comment %d: %v", it.id, err)
					failed++
					continue
				}
				c.Success("%s comment %d.", msg, it.id)
			}
			if failed > 0 {
				return cli.Exit(cli.ExitFailure)
			}
			return nil
		},
	})

	r.Add(cli.Command{
		Path:     "comment recount",
		Short:    "Recount the approved comments of posts (posts.comment_count).",
		Args:     []cli.Arg{{Name: "post-id", Usage: "Post ID(s).", Repeat: true}},
		Examples: []string{"comment recount 12 13"},
		Run: func(c *cli.Ctx) error {
			ids, err := c.ArgIDs(0)
			if err != nil {
				return err
			}
			s, err := e.Server(c.Context)
			if err != nil {
				return err
			}
			for _, id := range ids {
				p, err := contentGetPost(c.Context, s.store, id)
				if err != nil {
					return err
				}
				if err := s.store.RecountComments(c.Context, p.ID); err != nil {
					return err
				}
				s.contentPurgeComments(p.ID)
				p, _ = s.store.GetPost(c.Context, p.ID)
				if p != nil {
					c.Success("Updated post %d comment count to %d.", p.ID, p.CommentCount)
				}
			}
			return nil
		},
	})
}

// contentCommentCountRow is wp comment count's record from per-status counts.
func contentCommentCountRow(counts map[string]int) cli.Row {
	total := 0
	for _, n := range counts {
		total += n
	}
	return cli.Row{"approved": counts["approved"], "moderated": counts["pending"], "spam": counts["spam"],
		"trash": counts["trash"], "all": counts["approved"] + counts["pending"], "total_comments": total}
}

// contentApplyCommentFields copies the comment flags given into cm.
func contentApplyCommentFields(c *cli.Ctx, s *Server, cm *store.Comment) error {
	if v, ok := c.Lookup("comment_content"); ok {
		cm.Content = v
	}
	if v, ok := c.Lookup("comment_author"); ok {
		cm.AuthorName = v
	}
	if v, ok := c.Lookup("comment_author_email"); ok {
		cm.AuthorEmail = strings.TrimSpace(v)
	}
	if v, ok := c.Lookup("comment_author_url"); ok {
		cm.AuthorURL = strings.TrimSpace(v)
	}
	if v, ok := c.Lookup("comment_approved"); ok {
		st, valid := contentCommentStatus(v)
		if !valid || st == "all" {
			return cli.Usagef("invalid --comment_approved %q (1, 0, approve, hold, spam or trash)", v)
		}
		cm.Status = st
	}
	if v, ok := c.Lookup("comment_date"); ok {
		t, valid := contentParsePostDate(v, s.loc())
		if !valid {
			return cli.Usagef("invalid --comment_date %q (YYYY-MM-DD[ HH:MM:SS] or RFC 3339)", v)
		}
		cm.CreatedAt = t
	}
	if cm.CreatedAt.IsZero() {
		cm.CreatedAt = time.Now()
	}
	return nil
}

// contentCommentActions runs a moderation action (applyCommentAction, shared with the comments screen)
// on every comment ID argument.
func contentCommentActions(c *cli.Ctx, e *cliEnv, action, done string) error {
	ids, err := c.ArgIDs(0)
	if err != nil {
		return err
	}
	s, err := e.Server(c.Context)
	if err != nil {
		return err
	}
	failed := 0
	for _, id := range ids {
		if err := s.applyCommentAction(c.Context, id, action); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				err = cli.Errorf("Could not find the comment with ID %d.", id)
			}
			c.Warning("%v", err)
			failed++
			continue
		}
		c.Success("%s comment %d.", done, id)
	}
	if failed > 0 {
		return cli.Exit(cli.ExitFailure)
	}
	return nil
}

package store

import "context"

// checkHierarchyLoop is wp_check_post_hierarchy_for_loops() / wp_check_term_hierarchy_for_loops()
// for table ("posts" or "terms"): the parent to store when row id gets parent. A row can't be
// its own parent, and a parent that is one of its descendants (so the new link closes a loop) gives
// 0, i.e. top level. A loop already stored higher up the chain that does not involve id is broken
// by moving its members to the top level, as WordPress does, and parent is kept.
func checkHierarchyLoop(ctx context.Context, q DBTX, table string, id, parent int64) int64 {
	if parent <= 0 {
		return 0
	}
	if id == 0 { // a new row can't close a loop
		return parent
	}
	if parent == id {
		return 0
	}
	seen := map[int64]int{}
	var path []int64
	for cur := parent; cur > 0; {
		if cur == id {
			return 0
		}
		if at, ok := seen[cur]; ok {
			loop := path[at:]
			for _, m := range loop {
				_, _ = q.Exec(ctx, `UPDATE `+table+` SET parent_id = NULL WHERE id = $1`, m)
			}
			return parent
		}
		if len(path) >= 10000 {
			return parent
		}
		seen[cur] = len(path)
		path = append(path, cur)
		var next int64
		if err := q.QueryRow(ctx, `SELECT coalesce(parent_id, 0) FROM `+table+` WHERE id = $1`, cur).Scan(&next); err != nil {
			return parent
		}
		cur = next
	}
	return parent
}

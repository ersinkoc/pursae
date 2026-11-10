package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Products › Import in batches (WC_Product_CSV_Importer_Controller's AJAX import): the mapping screen's
// script posts step=batch again and again, each call imports gooImportBatchSize rows and answers with the
// progress, so a file of thousands of products never runs into a proxy time-out and an interrupted
// import shows where it stopped. The mapping, the position, the ids created so far and the log live
// in a state file next to the uploaded CSV. Without JavaScript the form still runs step=import in one go.

const gooImportBatchSize = 50

// gooImportStaleAfter is how long an abandoned upload (and its state file) stays in the temp directory.
const gooImportStaleAfter = 24 * time.Hour

type gooImportState struct {
	Mapping  []string         `json:"mapping"`
	Update   bool             `json:"update"`
	Position int              `json:"position"`
	IDMap    map[string]int64 `json:"id_map"`
	Result   gooImportResult  `json:"result"`
}

func gooImportStatePath(token string) string {
	return filepath.Join(os.TempDir(), strings.TrimSuffix(token, ".csv")+".state.json")
}

func gooLoadImportState(token string) (*gooImportState, bool) {
	raw, err := os.ReadFile(gooImportStatePath(token))
	if err != nil {
		return nil, false
	}
	st := &gooImportState{}
	if json.Unmarshal(raw, st) != nil {
		return nil, false
	}
	if st.IDMap == nil {
		st.IDMap = map[string]int64{}
	}
	return st, true
}

func gooSaveImportState(token string, st *gooImportState) error {
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return os.WriteFile(gooImportStatePath(token), raw, 0o600)
}

// gooImportBatch runs the next batch and writes the progress as JSON. The first call (no state file yet)
// takes the column mapping from the form.
func (s *Server) gooImportBatch(c *gin.Context, d *gooImportData) {
	fail := func(code int, msg string) { c.JSON(code, gin.H{"error": msg}) }
	token := c.PostForm("file")
	if !gooImportTokenRe.MatchString(token) {
		fail(http.StatusBadRequest, "Invalid file.")
		return
	}
	data, err := os.ReadFile(filepath.Join(os.TempDir(), token))
	if err != nil {
		fail(http.StatusNotFound, "The file does not exist, please try again.")
		return
	}
	rows, err := gooReadCSV(data, d.Delimiter)
	if err != nil || len(rows) == 0 {
		fail(http.StatusBadRequest, "The file is empty or using a different encoding than UTF-8, please try again with a new file.")
		return
	}
	st, ok := gooLoadImportState(token)
	if !ok {
		st = &gooImportState{Update: d.Update, IDMap: map[string]int64{}, Mapping: make([]string, len(rows[0]))}
		for i, h := range rows[0] {
			if v, ok := c.GetPostForm("map_to[" + strconv.Itoa(i) + "]"); ok {
				st.Mapping[i] = v
			} else {
				st.Mapping[i] = s.gooCSVHeaderKey(h)
			}
		}
	}
	body := rows[1:]
	idMap := map[int64]int64{}
	for k, v := range st.IDMap {
		if id, err := strconv.ParseInt(k, 10, 64); err == nil {
			idMap[id] = v
		}
	}
	from := min(st.Position, len(body))
	to := min(from+gooImportBatchSize, len(body))
	s.gooImportRowsRange(c.Request.Context(), body[from:to], from, st.Mapping, st.Update, currentUser(c).ID, idMap, &st.Result)
	st.Position = to
	st.IDMap = map[string]int64{}
	for k, v := range idMap {
		st.IDMap[strconv.FormatInt(k, 10)] = v
	}
	done := to >= len(body)
	if done {
		_ = os.Remove(filepath.Join(os.TempDir(), token))
		_ = os.Remove(gooImportStatePath(token))
	} else if err := gooSaveImportState(token, st); err != nil {
		fail(http.StatusInternalServerError, "The import progress could not be saved: "+err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"done": done, "position": to, "total": len(body), "imported": st.Result.Imported, "updated": st.Result.Updated,
		"skipped": st.Result.Skipped, "failed": st.Result.Failed, "log": st.Result.Log})
}

// gooImportCleanup deletes uploads (and state files) of imports nobody finished: the mapping screen
// stores the CSV in the temp directory and only the import step removes it.
func (s *Server) gooImportCleanup(_ context.Context) error {
	return gooImportCleanupDir(os.TempDir(), time.Now().Add(-gooImportStaleAfter))
}

func gooImportCleanupDir(dir string, before time.Time) error {
	var firstErr error
	for _, pat := range []string{"goo-import-*.csv", "goo-import-*.state.json"} {
		files, _ := filepath.Glob(filepath.Join(dir, pat))
		for _, f := range files {
			fi, err := os.Stat(f)
			if err != nil || fi.IsDir() || !fi.ModTime().Before(before) {
				continue
			}
			if err := os.Remove(f); err != nil && !os.IsNotExist(err) && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/gocdn"
	"wpgo/internal/media"
)

// GoCDN's admin screens: /wp-admin/gocdn?page=… (Storage, Media Offload, Image Service, Assets
// CDN, Purge, and Remote Storage under Media). Administrators only, only while the plugin is
// active. Secrets are never printed into a form: a saved secret shows as "saved" and is replaced
// by typing a new one or removed with its Clear box.

type gocdnAdminData struct {
	Page, Title string
	Nonce       string
	S           gocdnSettings
	Locked      map[string]bool
	EnvName     string
	Providers   []gocdnProvider
	Provider    *gocdnProvider
	SecretSaved bool
	TokenSaved  bool
	SSECSaved   bool
	PurgeCount  int
	Extras      []gocdnExtraView // additional purge providers (one per provider other than the main one)
	PurgerNames string           // "Cloudflare, BunnyCDN": every configured purge provider
	PurgeSaved  bool             // the secret of a non-Cloudflare purge provider
	Purgers     []gocdnPurger
	Purger      *gocdnPurger
	PurgeReady  bool
	AVIF        bool
	MoveFiles   int      // files in another bucket / under another path
	MoveFrom    []string // where they are
	Removed     int      // files only in the bucket
	Configured  bool
	Endpoint    string
	PublicBase  string
	Msg, Err    string
	Details     []string
	CacheOn     bool
	Home        string

	Stats    gocdnOffloadStats
	Job      gocdnJobState
	JobKinds map[string]string
	JobLabel string

	Remote *gocdnRemoteView

	Sample      string // the URL builder's result
	SamplePath  string
	SampleQuery string
	WebP        bool
}

func (d *gocdnAdminData) Lock(key string) bool { return d.Locked[key] }

func (s *Server) gocdnAdmin(c *gin.Context) {
	u := currentUser(c)
	if !u.Can("manage_options", nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to access this page.")
		return
	}
	page := c.Query("page")
	switch c.Query("action") {
	case "progress":
		s.gocdnProgress(c)
		return
	case "preview":
		s.gocdnRemotePreview(c)
		return
	case "remote-list":
		s.gocdnRemoteJSON(c)
		return
	case "remote-import":
		if c.Request.Method == http.MethodPost {
			s.gocdnRemoteImportJSON(c)
			return
		}
	}
	if c.Request.Method == http.MethodPost {
		if !s.checkNonce(c, gocdnNonce) {
			s.denyNonce(c)
			return
		}
		s.gocdnAdminPost(c, page)
		return
	}
	s.gocdnRender(c, page, "", "", nil)
}

func (s *Server) gocdnRender(c *gin.Context, page, msg, errMsg string, details []string) {
	st := s.gocdnLoad()
	d := &gocdnAdminData{Page: page, Nonce: s.nonce(c, gocdnNonce), Locked: gocdnLocked(), EnvName: gocdnEnv, Providers: gocdnProviders,
		Provider: gocdnProviderByKey(st.Provider), SecretSaved: st.SecretKey != "", TokenSaved: st.CFToken != "", SSECSaved: st.SSECKey != "", PurgeSaved: st.PurgeSecret != "",
		Purgers: gocdnPurgers, PurgeReady: s.gocdnPurgeReady(st), AVIF: gocdn.AVIFAvailable(),
		Configured: s.gocdnConfigured(st), Endpoint: gocdnEndpoint(st), PublicBase: gocdnPublicBase(st),
		Msg: msg, Err: errMsg, Details: details, CacheOn: s.settings.Bool("wpgo_cache_enabled"), Home: s.links(c).HomeURL("/"),
		JobKinds: gocdnJobKinds, WebP: gocdn.WebPAvailable()}
	if d.Msg == "" {
		d.Msg = c.Query("gocdn-msg")
	}
	if d.Err == "" {
		d.Err = c.Query("gocdn-error")
	}
	// Never print secrets.
	d.S = st
	d.S.SecretKey, d.S.CFToken, d.S.ImgKey, d.S.PurgeSecret, d.S.SSECKey, d.S.Previous, d.S.PurgeExtra = "", "", "", "", "", nil, nil
	if st.PurgeProvider != "" {
		d.Purger = gocdnPurgerByKey(st.PurgeProvider)
	}
	if page == "purge" {
		d.Extras = gocdnExtraViews(st)
		var names []string
		for _, t := range gocdnPurgeTargets(st) {
			names = append(names, gocdnPurgerByKey(t.Provider).Name)
		}
		d.PurgerNames = strings.Join(names, ", ")
		d.PurgeCount = len(names)
	}
	title := "Pursae CDN"
	switch page {
	case "offload":
		title = "Media Offload"
		d.Stats = s.gocdnStats(c)
		d.Job = s.gocdnJob()
		d.JobLabel = gocdnJobKinds[d.Job.Kind]
		d.MoveFiles, d.MoveFrom = s.gocdnMoveCount(c, st)
		d.Removed = d.Stats.Removed
	case "deactivate":
		title = "Deactivate Pursae CDN"
		d.Removed = s.gocdnRemovedCount(c)
		d.Job = s.gocdnJob()
	case "images":
		title = "Image Service"
		d.SamplePath, d.SampleQuery = c.Query("sample_path"), c.Query("sample_query")
		if d.SamplePath != "" {
			if p, err := gocdn.ParseImageParams(mustParseQuery(d.SampleQuery)); err != nil {
				d.Err = err.Error()
			} else {
				d.Sample = s.gocdnImageURL(c, strings.TrimLeft(d.SamplePath, "/"), p)
			}
		}
	case "assets":
		title = "Assets CDN"
	case "purge":
		title = "Purge"
	case "remote":
		title = "Remote Storage"
		d.Remote = s.gocdnRemoteList(c, st)
		if d.Remote.Err != "" && d.Err == "" {
			d.Err = d.Remote.Err
		}
	default:
		page, d.Page, title = "", "", "Pursae CDN Storage"
		d.MoveFiles, d.MoveFrom = s.gocdnMoveCount(c, st)
	}
	d.Title = title
	screen := "gocdn"
	if page != "" {
		screen += "?page=" + page
	}
	s.adminPage(c, screen, title, "toplevel_page_gocdn", "admin/gocdn", d)
}

func mustParseQuery(q string) url.Values {
	v, _ := url.ParseQuery(strings.TrimPrefix(q, "?"))
	return v
}

func (s *Server) gocdnAdminPost(c *gin.Context, page string) {
	back := "/wp-admin/gocdn"
	if page != "" {
		back += "?page=" + url.QueryEscape(page)
	}
	sep := "?"
	if strings.Contains(back, "?") {
		sep = "&"
	}
	done := func(msg string) { c.Redirect(http.StatusFound, back+sep+"gocdn-msg="+url.QueryEscape(msg)) }
	fail := func(msg string) { c.Redirect(http.StatusFound, back+sep+"gocdn-error="+url.QueryEscape(msg)) }
	st := s.gocdnLoad()
	locked := gocdnLocked()
	str := func(k string) string { return strings.TrimSpace(c.PostForm(k)) }
	setStr := func(key string, dst *string) {
		if !locked[key] {
			*dst = str(key)
		}
	}
	setBool := func(key string, dst *bool) {
		if !locked[key] {
			*dst = c.PostForm(key) != ""
		}
	}
	setInt := func(key string, dst *int) {
		if !locked[key] {
			*dst, _ = strconv.Atoi(str(key))
		}
	}
	setSecret := func(key string, dst *string) {
		if locked[key] {
			return
		}
		if c.PostForm(key+"-clear") != "" {
			*dst = ""
		} else if v := str(key); v != "" {
			*dst = v
		}
	}

	switch c.PostForm("do") {
	case "deactivate-download", "deactivate-keep":
		s.gocdnDeactivatePost(c, c.PostForm("do"))
		return
	case "save", "save-test":
		prev := st
		switch page {
		case "":
			setStr("provider", &st.Provider)
			setStr("endpoint", &st.Endpoint)
			setStr("region", &st.Region)
			setStr("bucket", &st.Bucket)
			setStr("access-key-id", &st.AccessKey)
			setSecret("secret-access-key", &st.SecretKey)
			setBool("use-path-style", &st.PathStyle)
			setStr("object-prefix", &st.Prefix)
			setBool("public-read", &st.PublicRead)
			setStr("delivery-domain", &st.CDNDomain)
			setStr("local-delivery", &st.LocalDelivery)
			if !locked["sse-type"] && !locked["server-side-encryption"] {
				if _, has := c.Request.PostForm["sse-type"]; has {
					st.SSEType = str("sse-type")
				} else {
					st.SSEType = "" // the first version's checkbox
					if c.PostForm("server-side-encryption") != "" {
						st.SSEType = "s3"
					}
				}
				st.SSE = st.SSEType == "s3"
			}
			setStr("sse-kms-key-id", &st.SSEKMSKey)
			if !locked["sse-c-key"] {
				if v := str("sse-c-key"); v != "" {
					k, err := gocdn.ParseSSECKey(v)
					if err != nil {
						fail(err.Error())
						return
					}
					st.SSECKey = hex.EncodeToString(k)
				} else if c.PostForm("sse-c-key-clear") != "" {
					st.SSECKey = ""
				}
			}
			setStr("storage-class", &st.StorageClass)
			setInt("multipart-threshold-mb", &st.MultipartMB)
			if v := strings.ToUpper(st.StorageClass); v != "" && !gocdnStorageClassRe.MatchString(v) {
				fail("The storage class is a name such as STANDARD_IA or INTELLIGENT_TIERING.")
				return
			}
			if st.Endpoint != "" {
				if u, err := url.Parse(st.Endpoint); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
					fail("The endpoint must be a URL such as https://s3.eu-central-1.amazonaws.com.")
					return
				}
			}
			if st.CDNDomain != "" && strings.ContainsAny(st.CDNDomain, " \"'<>") {
				fail("The delivery domain is not valid.")
				return
			}
			if st.SSEType != "" && st.SSEType != "s3" && st.SSEType != "kms" && st.SSEType != "customer" {
				fail("Unknown encryption type.")
				return
			}
			if st.SSEKMSKey != "" && !gocdnKMSKeyRe.MatchString(st.SSEKMSKey) {
				fail("The KMS key is a key ID, an alias such as alias/my-key or an ARN.")
				return
			}
			if st.SSEType == "customer" {
				if _, err := gocdn.ParseSSECKey(s.gocdnReveal(st.SSECKey)); err != nil {
					fail("SSE-C needs a customer key: 64 hex characters or 44 base64 characters (32 bytes).")
					return
				}
				if ep := gocdnEndpoint(st); !strings.HasPrefix(ep, "https://") && !strings.Contains(ep, "//127.0.0.1") && !strings.Contains(ep, "//localhost") {
					fail("SSE-C sends the key with every request and needs an https endpoint.")
					return
				}
			}
		case "offload":
			setBool("copy-to-s3", &st.CopyToBucket)
			setBool("remove-local-file", &st.RemoveLocal)
			setBool("serve-from-s3", &st.ServeFromCDN)
		case "images":
			setBool("img-enabled", &st.Img)
			setBool("img-sign", &st.ImgSign)
			setInt("img-max-width", &st.ImgMaxW)
			setInt("img-max-height", &st.ImgMaxH)
			setInt("img-quality", &st.ImgQuality)
			setBool("img-webp", &st.ImgWebP)
			setBool("img-cache-bucket", &st.ImgBucket)
			setBool("img-rewrite", &st.ImgRewrite)
			setInt("img-rewrite-quality", &st.ImgRewriteQ)
			setInt("img-max-source-mp", &st.ImgMaxSource)
			setBool("img-avif", &st.ImgAVIF)
			setInt("img-max-frames", &st.ImgMaxFrames)
		case "assets":
			setBool("assets-enabled", &st.Assets)
			setStr("assets-url", &st.AssetsURL)
			if !locked["assets-dirs"] {
				st.AssetsDirs = c.PostForm("assets-dirs")
			}
			setStr("assets-extensions", &st.AssetsExt)
			if !locked["assets-exclude"] {
				st.AssetsExclude = c.PostForm("assets-exclude")
			}
			if st.AssetsURL != "" {
				if u, err := url.Parse(st.AssetsURL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.RawQuery != "" {
					fail("The CDN URL must be an http(s) URL such as https://cdn.example.com.")
					return
				}
			}
			if st.Assets && st.AssetsURL == "" {
				fail("Enter the CDN URL before turning the assets CDN on.")
				return
			}
		case "purge":
			if !locked["purge-provider"] {
				st.PurgeProvider = str("purge-provider")
				if _, has := c.Request.PostForm["purge-provider"]; !has && c.PostForm("cloudflare") != "" { // the first version's form
					st.PurgeProvider = "cloudflare"
				}
				st.CF = st.PurgeProvider == "cloudflare"
			}
			if gocdnPurgerByKey(st.PurgeProvider) == nil {
				fail("Unknown CDN provider.")
				return
			}
			setStr("cloudflare-zone", &st.CFZone)
			setSecret("cloudflare-token", &st.CFToken)
			setBool("cloudflare-auto", &st.CFAuto)
			if st.PurgeProvider != "" && st.PurgeProvider != "cloudflare" {
				prevProvider := prev.PurgeProvider
				setStr("purge-zone", &st.PurgeZone)
				setStr("purge-access-key-id", &st.PurgeKeyID)
				if prevProvider != st.PurgeProvider && str("purge-secret") == "" && !locked["purge-secret"] {
					st.PurgeSecret = "" // another provider's key is no use
				}
				setSecret("purge-secret", &st.PurgeSecret)
				if p := gocdnPurgerByKey(st.PurgeProvider); st.PurgeZone != "" && !p.Zone.MatchString(st.PurgeZone) {
					fail(fmt.Sprintf("%s: the %s is not valid.", p.Name, s.__(c, p.ZoneLabel)))
					return
				}
			}
			if st.CFZone != "" && !gocdnZoneID.MatchString(st.CFZone) {
				fail("The Cloudflare zone ID is the 32-character ID on the zone's Overview page.")
				return
			}
			if c.PostForm("purge-extra-form") != "" && !locked["purge-extra"] {
				extras, msg := s.gocdnReadExtras(c, st)
				if msg != "" {
					fail(msg)
					return
				}
				st.PurgeExtra = extras
			}
		}
		if page == "" {
			st = s.gocdnRememberConn(c, prev, st)
		}
		if err := s.gocdnSave(c, st); err != nil {
			fail(err.Error())
			return
		}
		if c.PostForm("do") == "save-test" {
			ok, details := s.gocdnTest(c, s.gocdnLoad())
			if ok {
				s.gocdnRender(c, page, "Settings saved. Connection test passed.", "", details)
			} else {
				s.gocdnRender(c, page, "Settings saved.", "Connection test failed.", details)
			}
			return
		}
		done("Settings saved.")
	case "test":
		ok, details := s.gocdnTest(c, st)
		if ok {
			s.gocdnRender(c, page, "Connection test passed.", "", details)
		} else {
			s.gocdnRender(c, page, "", "Connection test failed.", details)
		}
	case "job":
		kind := c.PostForm("kind")
		if err := s.gocdnStartJobWith(c, kind, c.PostForm("resume") != "", gocdnJobOpts{DeleteOld: c.PostForm("delete-old") != ""}); err != nil {
			fail(err.Error())
			return
		}
		done(gocdnJobKinds[kind] + " started.")
	case "job-stop":
		s.gocdnStopJob()
		done("The job stops after the current attachment.")
	case "purge-cache":
		s.purgeAll(c)
		done("Pursae Cache purged.")
	case "purge-cloudflare", "purge-cdn":
		res := s.gocdnCDNPurgeEach(c, st, nil)
		if len(res) == 0 {
			fail("no CDN is configured for purging")
			return
		}
		var ok []string
		for _, r := range res {
			if r.Err == nil {
				ok = append(ok, r.Name)
			}
		}
		if err := gocdnPurgeError(res); err != nil {
			msg := fmt.Sprintf("Purge failed at %s.", err.Error())
			if len(ok) > 0 {
				msg = fmt.Sprintf("Purge failed at %s. Purged: %s.", err.Error(), strings.Join(ok, ", "))
			}
			fail(msg)
			return
		}
		done(strings.Join(ok, ", ") + " cache purged.")
	case "purge-images":
		if err := s.gocdnPurgeImageCache(); err != nil {
			fail(err.Error())
			return
		}
		done("Processed image cache emptied.")
	case "rotate-key":
		if locked["img-key"] {
			fail("The image key is defined in " + gocdnEnv + ".")
			return
		}
		st.ImgKey = gocdnRandomKey()
		if err := s.gocdnSave(c, st); err != nil {
			fail(err.Error())
			return
		}
		done("A new signing key was made; signed image URLs made with the old key no longer work.")
	case "import":
		n, skipped, err := s.gocdnImport(c, st, c.PostFormArray("keys[]"), c.PostForm("mode"))
		q := "&prefix=" + url.QueryEscape(c.PostForm("prefix"))
		msg := fmt.Sprintf("%d object(s) imported into the Media Library.", n)
		if len(skipped) > 0 {
			msg = fmt.Sprintf("%d object(s) imported into the Media Library. Skipped: %s", n, strings.Join(skipped, "; "))
		}
		if err != nil {
			c.Redirect(http.StatusFound, back+sep+"gocdn-error="+url.QueryEscape(err.Error()+" "+msg)+q)
			return
		}
		c.Redirect(http.StatusFound, back+sep+"gocdn-msg="+url.QueryEscape(msg)+q)
	default:
		fail("Unknown action.")
	}
}

// gocdnExtraView is one additional purge provider on the Purge screen: every provider other than
// the main one, enabled or not, with what is saved for it (never the secret).
type gocdnExtraView struct {
	Purger      *gocdnPurger
	Enabled     bool
	Zone, KeyID string
	SecretSaved bool
	Locked      bool
}

func gocdnExtraViews(st gocdnSettings) []gocdnExtraView {
	var out []gocdnExtraView
	for i := range gocdnPurgers {
		p := &gocdnPurgers[i]
		if p.Key == st.PurgeProvider {
			continue
		}
		v := gocdnExtraView{Purger: p, Locked: gocdnLocked()["purge-extra"]}
		for _, e := range st.PurgeExtra {
			if e.Provider == p.Key {
				v.Enabled, v.Zone, v.KeyID, v.SecretSaved = true, e.Zone, e.KeyID, e.Secret != ""
			}
		}
		out = append(out, v)
	}
	return out
}

// gocdnReadExtras reads the "Additional providers" part of the Purge form. A secret left empty
// keeps the saved one; the second result is an error message for the user.
func (s *Server) gocdnReadExtras(c *gin.Context, st gocdnSettings) ([]gocdnPurgeConf, string) {
	var out []gocdnPurgeConf
	for _, p := range gocdnPurgers {
		if p.Key == st.PurgeProvider || c.PostForm("extra-"+p.Key) == "" {
			continue
		}
		e := gocdnPurgeConf{Provider: p.Key, Zone: strings.TrimSpace(c.PostForm("extra-" + p.Key + "-zone")),
			KeyID: strings.TrimSpace(c.PostForm("extra-" + p.Key + "-key-id")), Secret: strings.TrimSpace(c.PostForm("extra-" + p.Key + "-secret"))}
		if e.Zone != "" && !p.Zone.MatchString(e.Zone) {
			return nil, fmt.Sprintf("%s: the %s is not valid.", p.Name, s.__(c, p.ZoneLabel))
		}
		if e.Secret == "" && c.PostForm("extra-"+p.Key+"-secret-clear") == "" {
			for _, old := range st.PurgeExtra {
				if old.Provider == p.Key {
					e.Secret = old.Secret
				}
			}
		}
		out = append(out, e)
	}
	return out, ""
}

// gocdnProgress is the JSON the Media Offload screen polls.
func (s *Server) gocdnProgress(c *gin.Context) {
	j := s.gocdnJob()
	pct := 0
	if j.Total > 0 {
		pct = (j.Done + j.Failed) * 100 / j.Total
	}
	errs := j.Errors
	if errs == nil {
		errs = []string{}
	}
	// The screen shows whole sentences in the user's language, like the rendered page.
	cat := s.catalog(c)
	state := ""
	switch {
	case j.Running && j.live:
		state = "running"
	case j.Finished:
		state = "finished"
	case j.Stopped:
		state = "stopped"
	case j.Running:
		state = "interrupted — start it again to resume"
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"kind": j.Kind, "label": cat.Text(gocdnJobKinds[j.Kind]), "running": j.Running && j.live, "total": j.Total, "done": j.Done,
		"failed": j.Failed, "files": j.Files, "percent": pct, "finished": j.Finished, "stopped": j.Stopped, "errors": errs,
		"resumable": j.Running && !j.live, "stats": s.gocdnStats(c),
		"count_text": cat.Text(fmt.Sprintf("%d done, %d failed of %d (%d files)", j.Done, j.Failed, j.Total, j.Files)), "state_text": cat.Text(state)})
}

// gocdnTest checks the connection with a probe object: PUT, HEAD, GET through the delivery URL
// (a warning only), DELETE. It returns readable steps.
func (s *Server) gocdnTest(c *gin.Context, st gocdnSettings) (bool, []string) {
	cl, err := s.gocdnClient(st)
	if err != nil {
		return false, []string{"Configuration: " + err.Error()}
	}
	ctx, cancel := context.WithTimeout(c, 45*time.Second)
	defer cancel()
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	key := st.Prefix + "gocdn-probe-" + hex.EncodeToString(b) + ".txt"
	body := []byte("Pursae CDN connection test " + time.Now().UTC().Format(time.RFC3339))
	var out []string
	opts := gocdn.PutOptions{ContentType: "text/plain", CacheControl: "no-store"}
	if st.PublicRead {
		opts.ACL = "public-read"
	}
	if err := cl.Put(ctx, key, body, opts); err != nil {
		return false, append(out, "Upload (PUT "+key+"): "+gocdnExplain(err))
	}
	out = append(out, "Upload (PUT "+key+"): OK")
	ok := true
	if o, err := cl.Head(ctx, key); err != nil {
		out = append(out, "Read back (HEAD): "+gocdnExplain(err))
		ok = false
	} else if o.Size != int64(len(body)) {
		out = append(out, fmt.Sprintf("Read back (HEAD): size %d, expected %d", o.Size, len(body)))
		ok = false
	} else {
		out = append(out, "Read back (HEAD): OK")
	}
	if pub := gocdnObjectURL(st, key); pub != "" {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, pub, nil)
		if resp, err := gocdnHTTP.Do(req); err != nil {
			out = append(out, "Public delivery URL: not reachable from the server ("+err.Error()+"). Check the delivery domain.")
		} else {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				out = append(out, "Public delivery URL: OK ("+gocdnPublicBase(st)+")")
			} else {
				out = append(out, fmt.Sprintf("Public delivery URL: %s answered %d. Make the bucket (or the objects) public, set a delivery domain, or deliver removed files with presigned URLs or the proxy.", gocdnPublicBase(st), resp.StatusCode))
			}
		}
	} else {
		out = append(out, "Public delivery URL: none. Enter a delivery domain (R2: the r2.dev URL or a custom domain).")
	}
	if err := cl.Delete(ctx, key); err != nil {
		out = append(out, "Delete (DELETE): "+gocdnExplain(err))
		ok = false
	} else {
		out = append(out, "Delete (DELETE): OK")
	}
	if _, err := cl.List(ctx, st.Prefix, "/", "", 1); err != nil {
		out = append(out, "List (ListObjectsV2): "+gocdnExplain(err)+" — the Remote Storage browser needs s3:ListBucket.")
	} else {
		out = append(out, "List (ListObjectsV2): OK")
	}
	return ok, out
}

// gocdnExplain turns storage errors into advice.
func gocdnExplain(err error) string {
	var se *gocdn.Error
	if errors.As(err, &se) {
		switch se.Code {
		case "SignatureDoesNotMatch":
			return se.Error() + " — the secret access key is wrong (or the region does not match)."
		case "InvalidAccessKeyId":
			return se.Error() + " — the access key ID does not exist for this provider."
		case "NoSuchBucket":
			return se.Error() + " — check the bucket name (and the region / endpoint)."
		case "AccessDenied":
			return se.Error() + " — the key has no permission for this bucket, or the ACL option is not allowed (turn \"public-read\" off for buckets that block ACLs)."
		case "AuthorizationHeaderMalformed", "PermanentRedirect", "IllegalLocationConstraintException":
			return se.Error() + " — the region does not match the bucket's region."
		case "RequestTimeTooSkewed":
			return se.Error() + " — the server's clock is wrong."
		}
		return se.Error()
	}
	var ne net.Error
	if errors.As(err, &ne) || strings.Contains(err.Error(), "no such host") || strings.Contains(err.Error(), "connection refused") {
		return err.Error() + " — the endpoint cannot be reached from the server."
	}
	return err.Error()
}

// --- remote storage browser ------------------------------------------------------------------

type gocdnRemoteObject struct {
	Key, Name, Size, Modified, Preview, URL string
	Image, InLibrary                        bool
}

type gocdnRemoteView struct {
	Prefix    string
	Crumbs    []struct{ Name, Prefix string }
	Folders   []struct{ Name, Prefix string }
	Objects   []gocdnRemoteObject
	Token     string
	NextToken string
	Err       string
}

func (s *Server) gocdnRemoteList(c *gin.Context, st gocdnSettings) *gocdnRemoteView {
	v := &gocdnRemoteView{Prefix: c.DefaultQuery("prefix", st.Prefix), Token: c.Query("token")}
	if strings.HasPrefix(v.Prefix, "/") {
		v.Prefix = strings.TrimLeft(v.Prefix, "/")
	}
	acc := ""
	for _, part := range strings.Split(strings.TrimSuffix(v.Prefix, "/"), "/") {
		if part == "" {
			continue
		}
		acc += part + "/"
		v.Crumbs = append(v.Crumbs, struct{ Name, Prefix string }{part, acc})
	}
	cl, err := s.gocdnClient(st)
	if err != nil {
		v.Err = "The storage is not configured: " + err.Error()
		return v
	}
	ctx, cancel := context.WithTimeout(c, 30*time.Second)
	defer cancel()
	res, err := cl.List(ctx, v.Prefix, "/", v.Token, 50)
	if err != nil {
		v.Err = gocdnExplain(err)
		return v
	}
	v.NextToken = res.NextToken
	for _, p := range res.Prefixes {
		v.Folders = append(v.Folders, struct{ Name, Prefix string }{strings.TrimSuffix(strings.TrimPrefix(p, v.Prefix), "/"), p})
	}
	var keys []string
	for _, o := range res.Objects {
		if strings.HasSuffix(o.Key, "/") {
			continue // folder marker
		}
		keys = append(keys, o.Key)
	}
	inLib := map[string]bool{}
	if rows, err := s.store.Pool.Query(c, `SELECT object_key FROM gocdn_items WHERE object_key = ANY($1) AND media_id IS NOT NULL`, keys); err == nil {
		for rows.Next() {
			var k string
			if rows.Scan(&k) == nil {
				inLib[k] = true
			}
		}
		rows.Close()
	}
	for _, o := range res.Objects {
		if strings.HasSuffix(o.Key, "/") {
			continue
		}
		ro := gocdnRemoteObject{Key: o.Key, Name: strings.TrimPrefix(o.Key, v.Prefix), Size: media.HumanSize(o.Size), InLibrary: inLib[o.Key],
			URL: gocdnObjectURL(st, o.Key)}
		if !o.LastModified.IsZero() {
			ro.Modified = o.LastModified.In(s.loc()).Format("2006-01-02 15:04")
		}
		if gocdnImageExt[strings.ToLower(pathExt(o.Key))] && o.Size <= 20<<20 {
			ro.Image = true
			ro.Preview = "/wp-admin/gocdn?page=remote&action=preview&key=" + url.QueryEscape(o.Key)
		}
		v.Objects = append(v.Objects, ro)
	}
	return v
}

func pathExt(p string) string {
	if i := strings.LastIndex(p, "."); i >= 0 && !strings.Contains(p[i:], "/") {
		return p[i:]
	}
	return ""
}

package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// installInputTag returns the <input …> tag with the given id.
func installInputTag(t *testing.T, body, id string) string {
	t.Helper()
	tag := regexp.MustCompile(`<input[^>]*\sid="` + regexp.QuoteMeta(id) + `"[^>]*>`).FindString(body)
	if tag == "" {
		t.Fatalf("no input #%s in the page", id)
	}
	return tag
}

// An installer error is announced (role="alert") and points at the field at fault: that input
// alone is aria-invalid, autofocused and described by the error (WCAG 4.1.3 / 3.3.1).
func TestInstallErrorAccessible(t *testing.T) {
	ts, _, srv := testServerBuilderOff(t)
	post := func(email string) string {
		resp, err := http.PostForm(ts.URL+"/wp-admin/install?step=2", url.Values{
			"setup_token": {testSetupToken}, "weblog_title": {"A11y"}, "user_name": {"admin"}, "admin_password": {"correct horse"}, "admin_email": {email},
		})
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}

	body := post("")
	if !strings.Contains(body, `id="install-error" role="alert"`) {
		t.Fatalf("the error is not announced:\n%s", body)
	}
	email := installInputTag(t, body, "admin_email")
	for _, want := range []string{`aria-invalid="true"`, "autofocus", `aria-describedby="install-error admin-email-desc"`} {
		if !strings.Contains(email, want) {
			t.Errorf("admin_email input lacks %s: %s", want, email)
		}
	}
	for _, id := range []string{"weblog_title", "user_login", "pass1", "pass2"} {
		if tag := installInputTag(t, body, id); strings.Contains(tag, "aria-invalid") || strings.Contains(tag, "autofocus") {
			t.Errorf("#%s is marked though it is valid: %s", id, tag)
		}
	}
	if pass := installInputTag(t, body, "pass1"); !strings.Contains(pass, `aria-describedby="pass-strength-result admin-password-desc"`) {
		t.Errorf("pass1 lost its descriptions: %s", pass)
	}

	// WordPress's invalid-email message keeps its <code> markup instead of showing escaped tags.
	body = post("not-an-email")
	if !strings.Contains(body, "<code>username@example.com</code>") || strings.Contains(body, "&lt;code&gt;") {
		t.Fatalf("invalid-email message markup:\n%s", body)
	}
	if email := installInputTag(t, body, "admin_email"); !strings.Contains(email, `aria-invalid="true"`) {
		t.Errorf("admin_email not marked invalid: %s", email)
	}

	render := func(v installView) string {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/wp-admin/install", nil)
		srv.renderAdminTemplate(c, http.StatusOK, "admin/install", v)
		return w.Body.String()
	}
	// The language step's error (a failed pack download) is announced too.
	if body := render(srv.installLanguageStep("The language pack could not be downloaded.")); !strings.Contains(body, `role="alert"`) {
		t.Errorf("language step error not announced:\n%s", body)
	}
	// An error carrying arbitrary text stays escaped.
	body = render(installView{Error: "Installation failed: <script>x</script>"})
	if strings.Contains(body, "<script>x</script>") || !strings.Contains(body, "&lt;script&gt;x&lt;/script&gt;") {
		t.Errorf("Installation failed error not escaped:\n%s", body)
	}
}

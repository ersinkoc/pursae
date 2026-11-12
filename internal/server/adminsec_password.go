package server

import (
	"context"
	_ "embed"
	"fmt"
	"math"
	"strings"
	"sync"
	"unicode"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
)

// Password policy: a minimum length and strength per role, the 10,000 most common passwords
// refused (the list WordPress' zxcvbn ships, de-obfuscated), and a forced change at the next login.

//go:embed adminsec_passwords.txt
var asCommonRaw string

var (
	asCommonOnce sync.Once
	asCommon     map[string]bool
)

func asIsCommon(pw string) bool {
	asCommonOnce.Do(func() {
		asCommon = make(map[string]bool, 10000)
		for _, l := range strings.Split(asCommonRaw, "\n") {
			if l = strings.TrimSpace(l); l != "" {
				asCommon[l] = true
			}
		}
	})
	return asCommon[strings.ToLower(pw)]
}

var asStrengthLabels = []string{"Very weak", "Weak", "Medium", "Strong", "Very strong"}

// asStrength scores a password 0-4, like zxcvbn's score: common passwords and passwords built
// from the user's own name score 0; otherwise the estimate is the character-pool entropy, cut for
// repeats, sequences and a common password with a few characters around it.
func asStrength(pw, login, email string) int {
	lower := strings.ToLower(pw)
	if pw == "" || asIsCommon(pw) {
		return 0
	}
	for _, v := range []string{strings.ToLower(login), strings.ToLower(strings.Split(email, "@")[0])} {
		if len(v) >= 3 && strings.Contains(lower, v) && len(pw)-len(v) < 6 {
			return 0
		}
	}
	var hasLower, hasUpper, hasDigit, hasOther bool
	for _, r := range pw {
		switch {
		case unicode.IsLower(r):
			hasLower = true
		case unicode.IsUpper(r):
			hasUpper = true
		case unicode.IsDigit(r):
			hasDigit = true
		default:
			hasOther = true
		}
	}
	pool := 0
	for _, x := range []struct {
		on bool
		n  int
	}{{hasLower, 26}, {hasUpper, 26}, {hasDigit, 10}, {hasOther, 33}} {
		if x.on {
			pool += x.n
		}
	}
	// Effective length: runs of one character, straight sequences and keyboard rows count little.
	runes := []rune(lower)
	walk := asKeyboardWalks(lower)
	eff := 0.0
	for i := range runes {
		switch {
		case walk[i]:
			eff += 0.3
		case i > 0 && runes[i] == runes[i-1]:
			eff += 0.2
		case i > 0 && (runes[i] == runes[i-1]+1 || runes[i] == runes[i-1]-1):
			eff += 0.3
		default:
			eff++
		}
	}
	bits := eff * math.Log2(float64(pool))
	// A common password with digits or symbols around it is barely stronger than the word.
	core := strings.TrimFunc(lower, func(r rune) bool { return !unicode.IsLetter(r) })
	if len(core) >= 4 && asIsCommon(core) {
		bits = math.Min(bits, 30)
	}
	switch {
	case bits < 28:
		return 0
	case bits < 40:
		return 1
	case bits < 56:
		return 2
	case bits < 72:
		return 3
	}
	return 4
}

// asPolicyFor is the strictest rule over the user's roles.
func (st asSettings) policyFor(roles []string) (minLen, minStrength int) {
	for _, r := range roles {
		minLen = max(minLen, st.PwMinLength[r])
		minStrength = max(minStrength, st.PwMinStrength[r])
	}
	return
}

// adminsecPasswordProblem returns why pw breaks the policy for a user with roles ("" when it is
// fine or the plugin is off).
func (s *Server) adminsecPasswordProblem(roles []string, login, email, pw string) string {
	if !s.adminsecOn() {
		return ""
	}
	st := s.asLoad()
	if st.PwAllowWeak {
		return ""
	}
	minLen, minStrength := st.policyFor(roles)
	if n := len([]rune(pw)); minLen > 0 && n < minLen {
		return fmt.Sprintf("The password must be at least %d characters long.", minLen)
	}
	if st.PwBlockCommon && asIsCommon(pw) {
		return "This password is one of the most commonly used passwords. Please choose another one."
	}
	if st.PwBreached && asPasswordBreached(context.Background(), pw) > 0 {
		return "This password has appeared in a data breach (Have I Been Pwned), so attackers try it first. Please choose another one."
	}
	if minStrength > 0 {
		if sc := asStrength(pw, login, email); sc < minStrength {
			return fmt.Sprintf("The password is too weak (%s). Use at least “%s”: a longer password with upper and lower case letters, numbers and symbols, not based on your name.",
				asStrengthLabels[sc], asStrengthLabels[minStrength])
		}
	}
	return ""
}

// adminsecUserPasswordProblem is adminsecPasswordProblem for an existing user.
func (s *Server) adminsecUserPasswordProblem(u *store.User, pw string) string {
	if u == nil {
		return ""
	}
	return s.adminsecPasswordProblem(u.Roles, u.Login, u.Email, pw)
}

// adminsecPasswordChanged runs after a user's password was set: application passwords and OAuth
// grants are revoked the way sessions are, then a pending forced change is done.
func (s *Server) adminsecPasswordChanged(c *gin.Context, u *store.User) {
	if u != nil {
		s.revokeCredentialsAfterPasswordChange(c, u.ID)
	}
	if !s.adminsecOn() || u == nil {
		return
	}
	if u.Meta["adminsec_force_password"] != nil {
		_ = s.setUserMeta(c, u, "adminsec_force_password", nil)
	}
	// A new password also ends every "Remember this device" trust: a stolen device must be challenged again.
	_, _ = s.store.Pool.Exec(c, `DELETE FROM adminsec_devices WHERE user_id = $1`, u.ID)
	s.asAudit(c, asEvent{Event: "password_changed", Severity: "notice", Object: u.Login, Message: "Password changed for " + u.Login + "."})
}

// revokeCredentialsAfterPasswordChange drops the user's application passwords and OAuth grants after a
// password change, mirroring endSessionsAfterPasswordChange. The application password or grant that made
// the change stays when it belongs to this user; every other credential is deleted.
func (s *Server) revokeCredentialsAfterPasswordChange(c *gin.Context, userID int64) {
	if userID == 0 {
		return
	}
	ctx := context.Background()
	var keepApp, keepGrant int64
	if c != nil {
		if c.Request != nil {
			ctx = c.Request.Context()
		}
		if v, ok := c.Get(restAppPasswordKey); ok {
			if id, ok := v.(int64); ok {
				keepApp = id
			}
		}
		if v, ok := c.Get(restOAuthKey); ok {
			if id, ok := v.(int64); ok {
				keepGrant = id
			}
		}
	}
	_ = s.revokeUserCredentials(ctx, userID, keepApp, keepGrant)
}

// revokeUserCredentials deletes the user's application passwords, OAuth grants and pending OAuth codes,
// except the application password keepApp and the grant keepGrant (0 keeps none). A deleted application
// password also stops matching through the match cache, which only serves hits that still have their row.
func (s *Server) revokeUserCredentials(ctx context.Context, userID, keepApp, keepGrant int64) error {
	if _, err := s.store.Pool.Exec(ctx, `DELETE FROM app_passwords WHERE user_id = $1 AND id <> $2`, userID, keepApp); err != nil {
		return err
	}
	if _, err := s.store.Pool.Exec(ctx, `DELETE FROM oauth_tokens WHERE user_id = $1 AND id <> $2`, userID, keepGrant); err != nil {
		return err
	}
	_, err := s.store.Pool.Exec(ctx, `DELETE FROM oauth_codes WHERE user_id = $1`, userID)
	return err
}

// asForcePassword reports a pending forced change: true when the policy asked for it, "admin"
// when an administrator did. With weak passwords allowed only the administrator's request stays,
// and the policy's request (not an administrator's) is skipped for a user with a second factor (2FA or MFA).
func (s *Server) asForcePassword(ctx context.Context, u *store.User) bool {
	switch v := u.Meta["adminsec_force_password"].(type) {
	case bool:
		// The policy's own request is skipped for users who sign in with a second factor.
		return v && !s.asLoad().PwAllowWeak && !s.asFactors(ctx, u.ID).any()
	case string:
		return v == "admin"
	}
	return false
}

var asKeyboardRows = []string{"qwertyuiop", "asdfghjkl", "zxcvbnm", "1234567890", "qwertzuiop", "azertyuiop", "!@#$%^&*()"}

// asKeyboardWalks marks the characters that belong to a run of four or more neighbouring keys
// ("qwerty", "asdf", "7890", also backwards).
func asKeyboardWalks(pw string) []bool {
	r := []rune(pw)
	out := make([]bool, len(r))
	for _, row := range asKeyboardRows {
		for _, line := range []string{row, reverseString(row)} {
			for i := 0; i < len(r); i++ {
				j := i
				for j < len(r) && strings.Contains(line, string(r[i:j+1])) {
					j++
				}
				if j-i >= 4 {
					for k := i; k < j; k++ {
						out[k] = true
					}
				}
			}
		}
	}
	return out
}

func reverseString(v string) string {
	r := []rune(v)
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return string(r)
}
